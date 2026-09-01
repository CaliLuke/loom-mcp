package codegen

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/service"
	goaexpr "github.com/CaliLuke/loom/expr"
)

// buildFieldDescriptions collects dotted field-path descriptions from the provided
// attribute. It follows objects, arrays, maps and user types, trimming any leading
// root qualifiers at error construction time (newValidationError does this for "body.").
func buildFieldDescriptions(att *goaexpr.AttributeExpr) map[string]string {
	if att == nil || att.Type == nil || att.Type == goaexpr.Empty {
		return nil
	}
	out := make(map[string]string)
	seen := make(map[string]struct{})
	var walk func(prefix string, a *goaexpr.AttributeExpr)
	walk = func(prefix string, a *goaexpr.AttributeExpr) {
		if a == nil || a.Type == nil || a.Type == goaexpr.Empty {
			return
		}
		switch dt := a.Type.(type) {
		case goaexpr.UserType:
			// Avoid infinite recursion on recursive user types.
			id := dt.ID()
			if _, ok := seen[id]; ok {
				return
			}
			seen[id] = struct{}{}
			walk(prefix, dt.Attribute())
		case *goaexpr.Object:
			for _, nat := range *dt {
				name := nat.Name
				path := name
				if prefix != "" {
					path = prefix + "." + name
				}
				if nat.Attribute != nil && nat.Attribute.Description != "" {
					out[path] = nat.Attribute.Description
				}
				walk(path, nat.Attribute)
			}
		case *goaexpr.Array:
			walk(prefix, dt.ElemType)
		case *goaexpr.Map:
			walk(prefix, dt.ElemType)
		case *goaexpr.Union:
			// Unions marshal as a canonical {type,value} object. Field paths should
			// reflect the actual wire contract to avoid misleading dotted paths like
			// "block.text" that omit the "value" envelope.
			valuePrefix := prefix
			if valueKey := dt.GetValueKey(); valueKey != "" {
				if valuePrefix != "" {
					valuePrefix = valuePrefix + "." + valueKey
				} else {
					valuePrefix = valueKey
				}
			}
			for _, v := range dt.Values {
				walk(valuePrefix, v.Attribute)
			}
		}
	}
	walk("", att)
	if len(out) == 0 {
		return nil
	}
	return out
}

// isEmptyStruct reports whether the attribute resolves to an empty object.
// It follows user types so callers can treat alias user types over empty
// objects the same as literal empty structs.
func isEmptyStruct(att *goaexpr.AttributeExpr) bool {
	if att == nil || att.Type == nil {
		return true
	}
	if att.Type == goaexpr.Empty {
		return true
	}
	switch dt := att.Type.(type) {
	case goaexpr.UserType:
		return isEmptyStruct(dt.Attribute())
	case *goaexpr.Object:
		return len(*dt) == 0
	default:
		return false
	}
}

// serviceName returns the declaring service name for a tool.
//
// Tool specs are provider-owned: they should identify the service that
// declares/implements the toolset, not the consuming agent service that happens
// to reference it.
func serviceName(tool *ToolData) string {
	ts := tool.Toolset
	if ts.SourceServiceName != "" {
		return ts.SourceServiceName
	}
	if ts.ServiceName != "" {
		return ts.ServiceName
	}
	return ""
}

// toolsetName returns the name of the toolset that contains the tool.
func toolsetName(tool *ToolData) string {
	return tool.Toolset.QualifiedName
}

// servicePkgAlias returns the import alias for the service package using the
// last path segment if available, falling back to the service PkgName.
func servicePkgAlias(svc *service.Data) string {
	// Always use the service package name so it matches the alias
	// used by Goa's NameScope when computing full type references.
	// Deriving the alias from the filesystem path (path.Base(PathName))
	// can diverge from the actual package identifier (e.g., underscores
	// vs. sanitized names), leading to mismatched qualifiers like
	// "atlasdataagent" vs "atlas_data_agent" in generated code.
	return svc.PkgName
}

// schemaForAttribute generates an inline JSON Schema for the given attribute.
// It returns the schema as JSON bytes, or nil if the attribute is empty or
// cannot be represented as a schema.
func schemaForAttribute(att *goaexpr.AttributeExpr) ([]byte, error) {
	if att == nil || att.Type == nil || att.Type == goaexpr.Empty {
		return nil, nil
	}
	schema, err := goaexpr.InlineJSONSchema(att)
	if err != nil {
		return nil, err
	}
	canonical := jsontext.Value(schema)
	if err := canonical.Canonicalize(); err != nil {
		return nil, fmt.Errorf("canonicalize inline JSON Schema: %w", err)
	}
	return []byte(canonical), nil
}

// authoredExampleForAttribute returns the last explicit Example(...) declared on
// the source attribute, normalized to the canonical JSON contract of target.
func authoredExampleForAttribute(source, target *goaexpr.AttributeExpr, path string) ([]byte, error) {
	if source == nil {
		return nil, nil
	}
	examples := source.ExtractUserExamples()
	if len(examples) == 0 {
		return nil, nil
	}
	return normalizeExampleValue(target, examples[len(examples)-1].Value, path)
}

// exampleForAttribute produces a minimal JSON example for the given attribute
// using Goa's example generator. When no meaningful example can be derived it
// returns nil so callers can distinguish between "no example" and an empty
// object.
func exampleForAttribute(att *goaexpr.AttributeExpr, path string) ([]byte, error) {
	if att == nil || att.Type == nil || att.Type == goaexpr.Empty {
		return nil, nil
	}
	gen := &goaexpr.ExampleGenerator{Randomizer: goaexpr.NewDeterministicRandomizer()}
	v := att.Example(gen)
	if v == nil {
		return nil, nil
	}
	return normalizeExampleValue(att, v, path)
}

// normalizeExampleValue canonicalizes one example value into JSON-native shapes
// and rewrites union nodes to the canonical {type,value} encoding.
func normalizeExampleValue(att *goaexpr.AttributeExpr, v any, path string) ([]byte, error) {
	// Normalize to JSON-native shapes (map[string]any, []any, float64, string, bool)
	// so downstream rewriting logic doesn't have to handle typed maps/slices that
	// Goa's example generator may produce for single-field objects.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	normalized, err = canonicalizeUnionExamples(att, normalized, path)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalized, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	// Treat "{}" as a non-informative example and omit it.
	if string(data) == "{}" {
		return nil, nil
	}
	return data, nil
}

// canonicalizeUnionExamples rewrites Goa's "flattened" union examples into the
// canonical JSON shape required by Goa-generated codecs: {type,value}.
//
// Goa's Union.Example returns only the selected branch value, which is useful
// for documentation but misleading for tool specs where the runtime decoder
// expects explicit discriminators. This helper preserves the structure produced
// by the standard example generator and wraps only union nodes.
func canonicalizeUnionExamples(att *goaexpr.AttributeExpr, example any, path string) (any, error) {
	if att == nil || att.Type == nil || att.Type == goaexpr.Empty {
		return example, nil
	}
	switch dt := att.Type.(type) {
	case goaexpr.UserType:
		return canonicalizeUnionExamples(dt.Attribute(), example, path)
	case *goaexpr.Object:
		m, ok := example.(map[string]any)
		if !ok {
			return example, nil
		}
		for k, v := range m {
			child := att.Find(k)
			if child == nil {
				delete(m, k)
				continue
			}
			childPath := joinExamplePath(path, k)
			var err error
			m[k], err = canonicalizeUnionExamples(child, v, childPath)
			if err != nil {
				return nil, err
			}
		}
		return m, nil
	case *goaexpr.Array:
		s, ok := example.([]any)
		if !ok {
			return example, nil
		}
		for i, v := range s {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			var err error
			s[i], err = canonicalizeUnionExamples(dt.ElemType, v, itemPath)
			if err != nil {
				return nil, err
			}
		}
		return s, nil
	case *goaexpr.Map:
		m, ok := example.(map[string]any)
		if !ok {
			return example, nil
		}
		for k, v := range m {
			itemPath := joinExamplePath(path, k)
			var err error
			m[k], err = canonicalizeUnionExamples(dt.ElemType, v, itemPath)
			if err != nil {
				return nil, err
			}
		}
		return m, nil
	case *goaexpr.Union:
		if example == nil || len(dt.Values) == 0 {
			return example, nil
		}

		var chosen *goaexpr.NamedAttributeExpr
		chosen = pickUnionVariantForExample(dt, example)
		if chosen == nil {
			return nil, fmt.Errorf("union example at %s does not match any variant for %q", path, dt.TypeName)
		}

		typeKey := dt.GetTypeKey()
		if typeKey == "" {
			typeKey = "type"
		}
		valueKey := dt.GetValueKey()
		if valueKey == "" {
			valueKey = "value"
		}

		value, err := canonicalizeUnionExamples(chosen.Attribute, example, joinExamplePath(path, valueKey))
		if err != nil {
			return nil, err
		}

		return map[string]any{
			typeKey:  chosen.Name,
			valueKey: value,
		}, nil
	default:
		return example, nil
	}
}

func joinExamplePath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func pickUnionVariantForExample(u *goaexpr.Union, example any) *goaexpr.NamedAttributeExpr {
	// Prefer key-based matching for object-shaped unions: Goa emits object examples
	// as map[string]any, but IsCompatible may not be able to match user type
	// variants directly (it reasons about Go types, not JSON shapes).
	if m, ok := example.(map[string]any); ok {
		for _, nat := range u.Values {
			if nat == nil || nat.Attribute == nil {
				continue
			}
			if unionVariantMatchesObjectKeys(nat.Attribute, m) {
				return nat
			}
		}
	}

	for _, nat := range u.Values {
		if nat == nil || nat.Attribute == nil || nat.Attribute.Type == nil {
			continue
		}
		attr := unwrapUserTypeAttr(nat.Attribute)
		if attr == nil || attr.Type == nil {
			continue
		}
		if attr.Type.IsCompatible(example) {
			return nat
		}
	}

	return nil
}

func unionVariantMatchesObjectKeys(att *goaexpr.AttributeExpr, example map[string]any) bool {
	attr := unwrapUserTypeAttr(att)
	if attr == nil {
		return false
	}
	obj, ok := attr.Type.(*goaexpr.Object)
	if !ok || obj == nil {
		return false
	}

	fields := make(map[string]struct{}, len(*obj))
	for _, nat := range *obj {
		fields[nat.Name] = struct{}{}
	}

	for k := range example {
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	return true
}

func unwrapUserTypeAttr(att *goaexpr.AttributeExpr) *goaexpr.AttributeExpr {
	if att == nil || att.Type == nil {
		return att
	}
	for {
		ut, ok := att.Type.(goaexpr.UserType)
		if !ok {
			return att
		}
		att = ut.Attribute()
		if att == nil || att.Type == nil {
			return att
		}
	}
}

// lowerCamel converts a string to lower camelCase using Goa's Goify function.
func lowerCamel(s string) string {
	return codegen.Goify(s, false)
}
