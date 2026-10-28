package codegen

import (
	"bytes"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom/expr"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// uuidCanonicalValue preserves enum and named-type occurrence constraints when
// proposing a deterministic UUID. The complete schema decides admissibility.
func uuidCanonicalValue(attr *expr.AttributeExpr) (any, bool) {
	var candidates []any
	var uuid bool
	for current := attr; current != nil; {
		if validation := current.Validation; validation != nil {
			candidates = append(candidates, validation.Values...)
			for _, format := range validation.Formats() {
				uuid = uuid || format == expr.FormatUUID
			}
		}
		named, ok := current.Type.(expr.UserType)
		if !ok {
			break
		}
		current = named.Attribute()
	}
	if !uuid {
		return nil, false
	}
	candidates = append(candidates, "00000000-0000-4000-8000-000000000001")
	return firstValidCanonicalValue(attr, candidates)
}

// preferredCanonicalValue uses authored examples in override order, followed
// by defaults. Validate against the occurrence schema, including named types.
func preferredCanonicalValue(attr *expr.AttributeExpr) (any, bool) {
	var candidates []any
	examples := attr.ExtractUserExamples()
	for i := len(examples) - 1; i >= 0; i-- {
		candidates = append(candidates, expr.CanonicalizeExample(attr, examples[i].Value))
	}
	for current := attr; current != nil; {
		if current.DefaultValue != nil {
			candidates = append(candidates, expr.CanonicalizeExample(attr, current.DefaultValue))
		}
		ut, ok := current.Type.(expr.UserType)
		if !ok {
			break
		}
		current = ut.Attribute()
	}
	if len(candidates) == 0 {
		return nil, false
	}
	return firstValidCanonicalValue(attr, candidates)
}

// canonicalNumericType unwraps aliases without losing occurrence constraints.
func canonicalNumericType(dataType expr.DataType) (expr.Primitive, bool) {
	if ut, ok := dataType.(expr.UserType); ok {
		return canonicalNumericType(ut.Attribute().Type)
	}
	primitive, ok := dataType.(expr.Primitive)
	if !ok {
		return 0, false
	}
	switch primitive {
	case expr.Int, expr.Int32, expr.Int64, expr.UInt, expr.UInt32, expr.UInt64, expr.Float32, expr.Float64:
		return primitive, true
	case expr.Boolean, expr.String, expr.Bytes, expr.Any:
		return 0, false
	}
	return 0, false
}

// numericCanonical tries enums, zero, and the nearest representable values at
// each bound. Full-schema validation enforces intersecting alias constraints.
func numericCanonical(attr *expr.AttributeExpr, primitive expr.Primitive) any {
	var enums, bounds []any
	for current := attr; current != nil; {
		if v := current.Validation; v != nil {
			enums = append(enums, v.Values...)
			for _, bound := range []struct {
				value     *expr.NumericBound
				lower     bool
				exclusive bool
			}{
				{v.Minimum, true, false}, {v.ExclusiveMinimum, true, true},
				{v.Maximum, false, false}, {v.ExclusiveMaximum, false, true},
			} {
				if bound.value != nil {
					bounds = append(bounds, canonicalNumericBounds(primitive, *bound.value, bound.lower, bound.exclusive)...)
				}
			}
		}
		ut, ok := current.Type.(expr.UserType)
		if !ok {
			break
		}
		current = ut.Attribute()
	}
	candidates := enums
	candidates = append(candidates, 0)
	candidates = append(candidates, bounds...)
	if value, ok := firstValidCanonicalValue(attr, candidates); ok {
		return value
	}
	panic(fmt.Sprintf("cannot synthesize a numeric recovery example for %s", attr.Type.Name()))
}

// canonicalNumericBounds prefers short tokens but also offers the exact native
// value when shortest-roundtrip JSON would cross the schema's exact endpoint.
func canonicalNumericBounds(primitive expr.Primitive, bound expr.NumericBound, lower, exclusive bool) []any {
	projection := expr.ProjectNumericBound(primitive.Kind(), bound, lower, exclusive)
	if projection.Empty {
		return nil
	}
	value := projection.Value
	if single, ok := value.(float32); ok {
		value = float64(single)
	}
	if _, ok := value.(float64); !ok {
		return []any{value}
	}
	exact, err := expr.NewNumericBound(value)
	if err != nil {
		panic(fmt.Errorf("encode projected recovery bound: %w", err))
	}
	return []any{value, exact}
}

// firstValidCanonicalValue validates the actual JSON representation, preserving
// 64-bit integer tokens. Schema construction failures are generator invariants.
func firstValidCanonicalValue(attr *expr.AttributeExpr, candidates []any) (any, bool) {
	schemaJSON, err := expr.InlineJSONSchema(attr)
	if err != nil {
		panic(fmt.Errorf("build recovery example schema: %w", err))
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		panic(fmt.Errorf("decode recovery example schema: %w", err))
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const schemaURL = "urn:loom-mcp:recovery-example"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		panic(fmt.Errorf("register recovery example schema: %w", err))
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		panic(fmt.Errorf("compile recovery example schema: %w", err))
	}
	for _, candidate := range candidates {
		encoded, err := json.Marshal(candidate, json.Deterministic(true))
		if err != nil {
			continue
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
		if err != nil {
			panic(fmt.Errorf("decode recovery example candidate: %w", err))
		}
		if schema.Validate(value) == nil {
			return candidate, true
		}
	}
	return nil, false
}
