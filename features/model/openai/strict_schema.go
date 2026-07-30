package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

const (
	strictSchemaTypeKey    = "type"
	strictSchemaTypeObject = "object"
	strictSchemaTypeString = "string"
	strictSchemaTypeNull   = "null"
)

var (
	strictUnsupportedKeywords = []string{"$schema", "example", "examples", "default"}

	strictSupportedStringFormats = map[string]struct{}{
		"date-time": {},
		"time":      {},
		"date":      {},
		"duration":  {},
		"email":     {},
		"hostname":  {},
		"ipv4":      {},
		"ipv6":      {},
		"uuid":      {},
	}

	strictChildSchemaListKeywords = []string{"anyOf", "oneOf", "allOf"}
	strictChildSchemaMapKeywords  = []string{"properties", "$defs", "definitions"}
)

// projectStrictSchema rewrites one canonical JSON Schema document into the
// subset OpenAI strict mode accepts.
func projectStrictSchema(schema rawjson.Message) (map[string]any, error) {
	data := bytes.TrimSpace(schema)
	if len(data) == 0 {
		return map[string]any{strictSchemaTypeKey: strictSchemaTypeObject, "additionalProperties": false}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON schema: %w", err)
	}
	if !includesStrictSchemaType(doc, strictSchemaTypeObject) {
		return nil, fmt.Errorf("schema root must declare type %q; OpenAI strict mode only accepts object payloads", strictSchemaTypeObject)
	}
	if err := projectStrictNode(doc, "$"); err != nil {
		return nil, err
	}
	return doc, nil
}

// canonicalizeStrictPayload restores the canonical encoding of absence in a
// strict-mode payload by dropping nulls that were introduced for optional fields.
func canonicalizeStrictPayload(schema, payload rawjson.Message) (rawjson.Message, error) {
	schemaData := bytes.TrimSpace(schema)
	payloadData := bytes.TrimSpace(payload)
	if len(schemaData) == 0 || len(payloadData) == 0 {
		return payload, nil
	}
	var root map[string]any
	if err := json.Unmarshal(schemaData, &root); err != nil {
		return nil, fmt.Errorf("invalid canonical schema: %w", err)
	}
	var doc any
	if err := json.Unmarshal(payloadData, &doc); err != nil {
		return nil, fmt.Errorf("invalid payload JSON: %w", err)
	}
	if !canonicalizeStrictValue(resolveStrictSchemas(root, root, nil), doc, root) {
		return payload, nil
	}
	normalized, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal canonicalized payload: %w", err)
	}
	return rawjson.Message(normalized), nil
}

func projectStrictNode(node map[string]any, path string) error {
	for _, keyword := range strictUnsupportedKeywords {
		delete(node, keyword)
	}
	projectStrictFormat(node)
	projectStrictUnion(node)
	if includesStrictSchemaType(node, strictSchemaTypeObject) {
		if err := projectStrictObject(node, path); err != nil {
			return err
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		if err := projectStrictNode(items, path+".items"); err != nil {
			return err
		}
	}
	for _, keyword := range strictChildSchemaListKeywords {
		branches, ok := node[keyword].([]any)
		if !ok {
			continue
		}
		for i, branch := range branches {
			branchMap, ok := branch.(map[string]any)
			if !ok {
				continue
			}
			if err := projectStrictNode(branchMap, fmt.Sprintf("%s.%s[%d]", path, keyword, i)); err != nil {
				return err
			}
		}
	}
	for _, keyword := range strictChildSchemaMapKeywords {
		children, ok := node[keyword].(map[string]any)
		if !ok {
			continue
		}
		for name, child := range children {
			childMap, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if err := projectStrictNode(childMap, path+"."+keyword+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

func projectStrictObject(node map[string]any, path string) error {
	switch additional := node["additionalProperties"].(type) {
	case nil:
		node["additionalProperties"] = false
	case bool:
		if additional {
			return fmt.Errorf("schema at %s declares an open object; OpenAI strict mode requires closed objects", path)
		}
	default:
		return fmt.Errorf("schema at %s declares a map-style object; OpenAI strict mode cannot represent open maps", path)
	}
	properties, ok := node["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		delete(node, "required")
		return nil
	}
	required := make(map[string]struct{})
	if names, ok := node["required"].([]any); ok {
		for _, name := range names {
			if s, ok := name.(string); ok {
				required[s] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, isRequired := required[name]; isRequired {
			continue
		}
		if property, ok := properties[name].(map[string]any); ok {
			makeStrictNullable(property)
		}
	}
	allRequired := make([]any, len(names))
	for i, name := range names {
		allRequired[i] = name
	}
	node["required"] = allRequired
	return nil
}

func projectStrictUnion(node map[string]any) {
	branches, ok := node["oneOf"].([]any)
	if !ok {
		return
	}
	delete(node, "oneOf")
	if existing, ok := node["anyOf"].([]any); ok {
		node["anyOf"] = append(existing, branches...)
		return
	}
	node["anyOf"] = branches
}

func projectStrictFormat(node map[string]any) {
	raw, present := node["format"]
	if !present {
		return
	}
	format, ok := raw.(string)
	if !ok || !includesStrictSchemaType(node, strictSchemaTypeString) {
		delete(node, "format")
		return
	}
	if _, supported := strictSupportedStringFormats[format]; !supported {
		delete(node, "format")
	}
}

func makeStrictNullable(property map[string]any) {
	projectStrictUnion(property)
	if enum, ok := property["enum"].([]any); ok && !containsJSONNull(enum) {
		property["enum"] = append(enum, nil)
	}
	switch declared := property[strictSchemaTypeKey].(type) {
	case string:
		if declared != strictSchemaTypeNull {
			property[strictSchemaTypeKey] = []any{declared, strictSchemaTypeNull}
		}
		return
	case []any:
		if !containsSchemaTypeName(declared, strictSchemaTypeNull) {
			property[strictSchemaTypeKey] = append(declared, strictSchemaTypeNull)
		}
		return
	}
	if branches, ok := property["anyOf"].([]any); ok {
		if !strictBranchesAcceptNull(branches) {
			property["anyOf"] = append(branches, map[string]any{strictSchemaTypeKey: strictSchemaTypeNull})
		}
		return
	}
	if ref, ok := property["$ref"]; ok {
		delete(property, "$ref")
		property["anyOf"] = []any{
			map[string]any{"$ref": ref},
			map[string]any{strictSchemaTypeKey: strictSchemaTypeNull},
		}
	}
}

func canonicalizeStrictValue(candidates []map[string]any, value any, root map[string]any) bool {
	changed := false
	switch actual := value.(type) {
	case map[string]any:
		for name, member := range actual {
			memberCandidates := memberStrictSchemas(candidates, name, root)
			if member == nil {
				if len(memberCandidates) > 0 && !strictSchemasAcceptNull(memberCandidates) {
					delete(actual, name)
					changed = true
				}
				continue
			}
			if canonicalizeStrictValue(memberCandidates, member, root) {
				changed = true
			}
		}
	case []any:
		itemCandidates := itemStrictSchemas(candidates, root)
		for _, element := range actual {
			if canonicalizeStrictValue(itemCandidates, element, root) {
				changed = true
			}
		}
	}
	return changed
}

func resolveStrictSchemas(node map[string]any, root map[string]any, seen map[string]struct{}) []map[string]any {
	if node == nil {
		return nil
	}
	if ref, ok := node["$ref"].(string); ok {
		if _, cycling := seen[ref]; cycling {
			return nil
		}
		target := resolveLocalSchemaRef(root, ref)
		if target == nil {
			return nil
		}
		next := make(map[string]struct{}, len(seen)+1)
		for key := range seen {
			next[key] = struct{}{}
		}
		next[ref] = struct{}{}
		return resolveStrictSchemas(target, root, next)
	}
	var out []map[string]any
	branched := false
	for _, keyword := range strictChildSchemaListKeywords {
		branches, ok := node[keyword].([]any)
		if !ok {
			continue
		}
		branched = true
		for _, branch := range branches {
			if branchMap, ok := branch.(map[string]any); ok {
				out = append(out, resolveStrictSchemas(branchMap, root, seen)...)
			}
		}
	}
	if hasDirectStrictConstraints(node) || !branched {
		out = append(out, node)
	}
	return out
}

func memberStrictSchemas(candidates []map[string]any, name string, root map[string]any) []map[string]any {
	var out []map[string]any
	for _, candidate := range candidates {
		properties, ok := candidate["properties"].(map[string]any)
		if !ok {
			continue
		}
		member, ok := properties[name].(map[string]any)
		if !ok {
			continue
		}
		out = append(out, resolveStrictSchemas(member, root, nil)...)
	}
	return out
}

func itemStrictSchemas(candidates []map[string]any, root map[string]any) []map[string]any {
	var out []map[string]any
	for _, candidate := range candidates {
		if items, ok := candidate["items"].(map[string]any); ok {
			out = append(out, resolveStrictSchemas(items, root, nil)...)
		}
	}
	return out
}

func strictSchemasAcceptNull(candidates []map[string]any) bool {
	return slices.ContainsFunc(candidates, strictSchemaAcceptsNull)
}

func strictSchemaAcceptsNull(schema map[string]any) bool {
	if enum, ok := schema["enum"].([]any); ok {
		return containsJSONNull(enum)
	}
	switch declared := schema[strictSchemaTypeKey].(type) {
	case string:
		return declared == strictSchemaTypeNull
	case []any:
		return containsSchemaTypeName(declared, strictSchemaTypeNull)
	}
	return true
}

func hasDirectStrictConstraints(schema map[string]any) bool {
	for _, keyword := range []string{strictSchemaTypeKey, "enum", "properties", "items"} {
		if _, ok := schema[keyword]; ok {
			return true
		}
	}
	return false
}

func strictBranchesAcceptNull(branches []any) bool {
	return slices.ContainsFunc(branches, func(branch any) bool {
		branchMap, ok := branch.(map[string]any)
		return ok && includesStrictSchemaType(branchMap, strictSchemaTypeNull)
	})
}

func resolveLocalSchemaRef(root map[string]any, ref string) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	node := any(root)
	for segment := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		current, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = current[segment]
	}
	target, _ := node.(map[string]any)
	return target
}

func includesStrictSchemaType(node map[string]any, want string) bool {
	switch declared := node[strictSchemaTypeKey].(type) {
	case string:
		return declared == want
	case []any:
		return containsSchemaTypeName(declared, want)
	}
	return false
}

func containsSchemaTypeName(types []any, want string) bool {
	return slices.ContainsFunc(types, func(entry any) bool {
		name, ok := entry.(string)
		return ok && name == want
	})
}

func containsJSONNull(values []any) bool {
	return slices.Contains(values, nil)
}
