package codegen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/CaliLuke/loom/expr"
)

// UnionEnvelopeMeta captures, for one top-level payload field whose type is a
// discriminated union, the data the per-tool recovery hint needs to build a
// DSL-faithful example.
type UnionEnvelopeMeta struct {
	// FieldName is the outer payload field that holds the union envelope
	// (e.g. "request" when the payload declares Attribute("request", OneOf(...))).
	FieldName string
	// TypeKey is the discriminator field name inside the envelope (defaults to "type").
	TypeKey string
	// ValueKey is the inner branch payload key inside the envelope (defaults to "value").
	ValueKey string
	// Tags lists the declared discriminator values in DSL source order.
	Tags []string
	// FirstTag is the deterministic substitution tag used when the caller
	// supplied an invalid discriminator. Equal to Tags[0] when Tags is non-empty.
	FirstTag string
	// TagExamples maps each declared tag to the canonical full-payload JSON
	// example that uses that tag for this envelope. When the caller supplies a
	// valid tag but some other field is malformed, the recovery hint reuses the
	// caller's tag by looking it up here.
	TagExamples map[string]string
}

// synthesizeCanonicalExample produces a deterministic, always-valid JSON
// example for the given payload attribute. Unlike buildExampleJSON, this walker
// commits to: first declared union branch, first declared enum value, and
// type-appropriate zero values for primitives. The output is intended for
// inclusion in tool recovery hints; callers expect it to round-trip through
// the same decoder that produced the validation error.
func synthesizeCanonicalExample(attr *expr.AttributeExpr) string {
	if attr == nil || attr.Type == nil || attr.Type == expr.Empty {
		return "{}"
	}
	v := canonicalValue(attr, map[string]bool{})
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// collectUnionEnvelopes returns one UnionEnvelopeMeta per top-level object
// attribute whose type is a discriminated union. Returns nil when the payload
// has no union envelope fields. Order matches DSL declaration order. The
// payload attribute is required because tag-specific examples are full payload
// envelopes — they need to be valid against the same decoder that rejected the
// caller's input, which means the other top-level fields must be present.
func collectUnionEnvelopes(payload *expr.AttributeExpr) []UnionEnvelopeMeta {
	if payload == nil || payload.Type == nil {
		return nil
	}
	obj := asObject(payload)
	if obj == nil {
		return nil
	}
	out := make([]UnionEnvelopeMeta, 0, len(*obj))
	for _, nat := range *obj {
		if nat == nil || nat.Attribute == nil {
			continue
		}
		u, ok := unwrapUnion(nat.Attribute.Type)
		if !ok {
			continue
		}
		tags := unionTags(u)
		first := ""
		if len(tags) > 0 {
			first = tags[0]
		}
		out = append(out, UnionEnvelopeMeta{
			FieldName:   nat.Name,
			TypeKey:     u.GetTypeKey(),
			ValueKey:    u.GetValueKey(),
			Tags:        tags,
			FirstTag:    first,
			TagExamples: tagExamples(payload, nat.Name, u),
		})
	}
	return out
}

// tagExamples builds, for each declared tag of the union, a full-payload
// canonical JSON example where the named envelope field uses that tag. Other
// payload fields are filled with their canonical values so the example
// round-trips through the strict decoder.
func tagExamples(payload *expr.AttributeExpr, fieldName string, u *expr.Union) map[string]string {
	out := map[string]string{}
	for _, nat := range u.Values {
		tag := expr.UnionVariantTag(nat)
		if tag == "" {
			continue
		}
		v := canonicalValue(payload, map[string]bool{})
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		branch := canonicalValue(nat.Attribute, map[string]bool{})
		if branch == nil {
			branch = map[string]any{}
		}
		obj[fieldName] = map[string]any{
			u.GetTypeKey():  tag,
			u.GetValueKey(): branch,
		}
		b, err := json.Marshal(obj)
		if err != nil {
			continue
		}
		out[tag] = string(b)
	}
	return out
}

// FormatTagList renders the declared tag set as a quoted, comma-separated list
// suitable for embedding in a recovery hint message.
func FormatTagList(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	quoted := make([]string, len(tags))
	for i, t := range tags {
		quoted[i] = fmt.Sprintf("%q", t)
	}
	return strings.Join(quoted, ", ")
}

// canonicalValue walks the attribute tree and returns a JSON-marshalable value
// that the JSON decoder for the generated Go type will accept.
func canonicalValue(attr *expr.AttributeExpr, seen map[string]bool) any {
	if attr == nil || attr.Type == nil {
		return nil
	}
	switch t := attr.Type.(type) {
	case expr.Primitive:
		return primitiveCanonical(attr, t)
	case *expr.Array:
		return []any{}
	case *expr.Map:
		return map[string]any{}
	case *expr.Object:
		return objectCanonical(attr, t, seen)
	case *expr.Union:
		return unionCanonical(t, seen)
	case expr.UserType:
		id := t.ID()
		if seen[id] {
			return map[string]any{}
		}
		seen[id] = true
		defer delete(seen, id)
		return canonicalValue(t.Attribute(), seen)
	default:
		return nil
	}
}

func primitiveCanonical(attr *expr.AttributeExpr, p expr.Primitive) any {
	if attr != nil && attr.Validation != nil && len(attr.Validation.Values) > 0 {
		return attr.Validation.Values[0]
	}
	switch p.Kind() {
	case expr.BooleanKind:
		return false
	case expr.IntKind, expr.Int32Kind, expr.Int64Kind,
		expr.UIntKind, expr.UInt32Kind, expr.UInt64Kind:
		return 0
	case expr.Float32Kind, expr.Float64Kind:
		return 0
	case expr.StringKind:
		// The runtime required-field validator rejects "" for required
		// strings, so emitting an empty string in the canonical example
		// would produce a hint that a client cannot follow without
		// looping on the same error. Use a non-empty placeholder.
		return "example"
	case expr.BytesKind:
		return "example"
	case expr.AnyKind:
		return nil
	case expr.ArrayKind, expr.ObjectKind, expr.MapKind,
		expr.UnionKind, expr.UserTypeKind, expr.ResultTypeKind:
		// Non-primitive kinds are handled by the calling switch in
		// canonicalValue; reaching here means the IR violated the
		// expr.Primitive contract.
		return nil
	}
	return nil
}

func objectCanonical(attr *expr.AttributeExpr, obj *expr.Object, seen map[string]bool) map[string]any {
	out := map[string]any{}
	required := requiredSet(attr)
	names := make([]string, 0, len(*obj))
	for _, nat := range *obj {
		if nat == nil || nat.Attribute == nil {
			continue
		}
		if _, ok := required[nat.Name]; !ok && nat.Attribute.DefaultValue == nil {
			continue
		}
		names = append(names, nat.Name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		return indexOfField(*obj, names[i]) < indexOfField(*obj, names[j])
	})
	for _, name := range names {
		nat := lookupField(*obj, name)
		if nat == nil {
			continue
		}
		out[name] = canonicalValue(nat.Attribute, seen)
	}
	return out
}

func unionCanonical(u *expr.Union, seen map[string]bool) map[string]any {
	tags := unionTags(u)
	if len(tags) == 0 || len(u.Values) == 0 {
		return map[string]any{}
	}
	first := u.Values[0]
	branch := canonicalValue(first.Attribute, seen)
	if branch == nil {
		branch = map[string]any{}
	}
	return map[string]any{
		u.GetTypeKey():  tags[0],
		u.GetValueKey(): branch,
	}
}

func unionTags(u *expr.Union) []string {
	if u == nil {
		return nil
	}
	out := make([]string, 0, len(u.Values))
	for _, nat := range u.Values {
		tag := expr.UnionVariantTag(nat)
		if tag == "" {
			continue
		}
		out = append(out, tag)
	}
	return out
}

func unwrapUnion(t expr.DataType) (*expr.Union, bool) {
	switch v := t.(type) {
	case *expr.Union:
		return v, true
	case expr.UserType:
		if v.Attribute() == nil {
			return nil, false
		}
		return unwrapUnion(v.Attribute().Type)
	}
	return nil, false
}

func asObject(attr *expr.AttributeExpr) *expr.Object {
	if attr == nil || attr.Type == nil {
		return nil
	}
	switch t := attr.Type.(type) {
	case *expr.Object:
		return t
	case expr.UserType:
		if t.Attribute() == nil {
			return nil
		}
		return asObject(t.Attribute())
	}
	return nil
}

func requiredSet(attr *expr.AttributeExpr) map[string]struct{} {
	out := map[string]struct{}{}
	if attr == nil {
		return out
	}
	collectRequired(attr, out)
	if ut, ok := attr.Type.(expr.UserType); ok && ut.Attribute() != nil {
		collectRequired(ut.Attribute(), out)
	}
	return out
}

func collectRequired(attr *expr.AttributeExpr, out map[string]struct{}) {
	if attr == nil || attr.Validation == nil {
		return
	}
	for _, name := range attr.Validation.Required {
		out[name] = struct{}{}
	}
}

func indexOfField(obj expr.Object, name string) int {
	for i, nat := range obj {
		if nat != nil && nat.Name == name {
			return i
		}
	}
	return len(obj)
}

func lookupField(obj expr.Object, name string) *expr.NamedAttributeExpr {
	for _, nat := range obj {
		if nat != nil && nat.Name == name {
			return nat
		}
	}
	return nil
}
