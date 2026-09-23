package codegen

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"math"
	"math/big"
	"strconv"

	"github.com/CaliLuke/loom/expr"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

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
				value     *float64
				lower     bool
				exclusive bool
			}{
				{v.Minimum, true, false}, {v.ExclusiveMinimum, true, true},
				{v.Maximum, false, false}, {v.ExclusiveMaximum, false, true},
			} {
				if bound.value != nil {
					bounds = append(bounds, canonicalNumericBound(primitive, *bound.value, bound.lower, bound.exclusive))
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

func canonicalNumericBound(primitive expr.Primitive, bound float64, lower, exclusive bool) any {
	direction := math.Inf(-1)
	if lower {
		direction = math.Inf(1)
	}
	if primitive == expr.Float32 {
		value := float32(bound)
		if (lower && float64(value) < bound) || (!lower && float64(value) > bound) || (exclusive && float64(value) == bound) {
			value = math.Nextafter32(value, float32(direction))
		}
		// Preserve the actual float32 value on the wire. Its shortest float32
		// decimal can lie outside the bound even when the binary value does not.
		return float64(value)
	}
	if primitive == expr.Float64 {
		if exclusive {
			return math.Nextafter(bound, direction)
		}
		return bound
	}
	// Match the decimal bound advertised in JSON, which can differ from the
	// exact binary float value. Integer arithmetic also preserves units above 2^53.
	rational, ok := new(big.Rat).SetString(strconv.FormatFloat(bound, 'g', -1, 64))
	if !ok {
		panic(fmt.Sprintf("invalid numeric recovery bound: %v", bound))
	}
	integer, remainder := new(big.Int), new(big.Int)
	integer.QuoRem(rational.Num(), rational.Denom(), remainder)
	if lower && (remainder.Sign() > 0 || (exclusive && remainder.Sign() == 0)) {
		integer.Add(integer, big.NewInt(1))
	}
	if !lower && (remainder.Sign() < 0 || (exclusive && remainder.Sign() == 0)) {
		integer.Sub(integer, big.NewInt(1))
	}
	return jsontext.Value(integer.String())
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
