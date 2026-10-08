package codegen

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

func TestSchemaValidationSharedHelperResolvesLocalRefsAndUnicodeLengths(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"label":{"$ref":"#/$defs/Label"}},"required":["label"],"$defs":{"Label":{"type":"string","minLength":2,"maxLength":2}}}`)
	if err := tools.ValidateJSONSchema(schema, map[string]any{"label": "你好"}); err != nil {
		t.Fatalf("expected valid two-code-point string: %v", err)
	}
	if err := tools.ValidateJSONSchema(schema, map[string]any{"label": "界"}); err == nil {
		t.Fatal("expected one-code-point string to violate minLength")
	}
}

// TestSchemaValidationRejectsInvalidPayloadsProperty verifies Property 7:
// Schema Validation Rejects Invalid Payloads.
// **Feature: mcp-registry, Property 7: Schema Validation Rejects Invalid Payloads**
// *For any* tool with a JSON schema, payloads that violate the schema SHALL be
// rejected before invocation.
// **Validates: Requirements 2.2**
func TestSchemaValidationRejectsInvalidPayloadsProperty(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 100
	properties := gopter.NewProperties(parameters)

	properties.Property("invalid payloads are rejected by schema validation", prop.ForAll(
		func(tc invalidPayloadTestCase) bool {
			err := tools.ValidateJSONSchema(tc.Schema, tc.Payload)
			// Property: invalid payloads MUST be rejected (err != nil)
			return err != nil
		},
		genInvalidPayloadTestCase(),
	))

	properties.TestingRun(t)
}

// TestSchemaValidationAcceptsValidPayloadsProperty verifies that valid payloads
// are accepted by schema validation.
// **Feature: mcp-registry, Property 7: Schema Validation Rejects Invalid Payloads**
// **Validates: Requirements 2.2**
func TestSchemaValidationAcceptsValidPayloadsProperty(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 100
	properties := gopter.NewProperties(parameters)

	properties.Property("valid payloads are accepted by schema validation", prop.ForAll(
		func(tc validPayloadTestCase) bool {
			err := tools.ValidateJSONSchema(tc.Schema, tc.Payload)
			// Property: valid payloads MUST be accepted (err == nil)
			return err == nil
		},
		genValidPayloadTestCase(),
	))

	properties.TestingRun(t)
}

// TestSchemaValidationRejectsMissingRequiredFields verifies that missing required
// fields are rejected.
// **Feature: mcp-registry, Property 7: Schema Validation Rejects Invalid Payloads**
// **Validates: Requirements 2.2**
func TestSchemaValidationRejectsMissingRequiredFields(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 100
	properties := gopter.NewProperties(parameters)

	properties.Property("missing required fields are rejected", prop.ForAll(
		func(tc missingRequiredFieldTestCase) bool {
			err := tools.ValidateJSONSchema(tc.Schema, tc.Payload)
			// Property: payloads missing required fields MUST be rejected
			return err != nil
		},
		genMissingRequiredFieldTestCase(),
	))

	properties.TestingRun(t)
}

// TestSchemaValidationRejectsWrongTypes verifies that wrong types are rejected.
// **Feature: mcp-registry, Property 7: Schema Validation Rejects Invalid Payloads**
// **Validates: Requirements 2.2**
func TestSchemaValidationRejectsWrongTypes(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 100
	properties := gopter.NewProperties(parameters)

	properties.Property("wrong types are rejected", prop.ForAll(
		func(tc wrongTypeTestCase) bool {
			err := tools.ValidateJSONSchema(tc.Schema, tc.Payload)
			// Property: payloads with wrong types MUST be rejected
			return err != nil
		},
		genWrongTypeTestCase(),
	))

	properties.TestingRun(t)
}

// TestSchemaValidationRejectsConstraintViolations verifies that constraint
// violations are rejected.
// **Feature: mcp-registry, Property 7: Schema Validation Rejects Invalid Payloads**
// **Validates: Requirements 2.2**
func TestSchemaValidationRejectsConstraintViolations(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 100
	properties := gopter.NewProperties(parameters)

	properties.Property("constraint violations are rejected", prop.ForAll(
		func(tc constraintViolationTestCase) bool {
			err := tools.ValidateJSONSchema(tc.Schema, tc.Payload)
			// Property: payloads violating constraints MUST be rejected
			return err != nil
		},
		genConstraintViolationTestCase(),
	))

	properties.TestingRun(t)
}

// Test case types

type invalidPayloadTestCase struct {
	Schema  []byte
	Payload any
}

type validPayloadTestCase struct {
	Schema  []byte
	Payload any
}

type missingRequiredFieldTestCase struct {
	Schema  []byte
	Payload any
}

type wrongTypeTestCase struct {
	Schema  []byte
	Payload any
}

type constraintViolationTestCase struct {
	Schema  []byte
	Payload any
}

// Generators

func genInvalidPayloadTestCase() gopter.Gen {
	return gen.OneGenOf(
		genMissingRequiredFieldTestCase().Map(func(tc missingRequiredFieldTestCase) invalidPayloadTestCase {
			return invalidPayloadTestCase(tc)
		}),
		genWrongTypeTestCase().Map(func(tc wrongTypeTestCase) invalidPayloadTestCase {
			return invalidPayloadTestCase(tc)
		}),
		genConstraintViolationTestCase().Map(func(tc constraintViolationTestCase) invalidPayloadTestCase {
			return invalidPayloadTestCase(tc)
		}),
	)
}

func genValidPayloadTestCase() gopter.Gen {
	return gen.OneGenOf(
		genValidStringPayload(),
		genValidIntegerPayload(),
		genValidObjectPayload(),
		genValidArrayPayload(),
		genValidBooleanPayload(),
	)
}

func genValidStringPayload() gopter.Gen {
	return gen.AlphaString().
		SuchThat(func(s string) bool { return len(s) >= 1 && len(s) <= 100 }).
		Map(func(s string) validPayloadTestCase {
			schema := []byte(`{"type":"string","minLength":1,"maxLength":100}`)
			return validPayloadTestCase{Schema: schema, Payload: s}
		})
}

func genValidIntegerPayload() gopter.Gen {
	return gen.IntRange(0, 100).Map(func(n int) validPayloadTestCase {
		schema := []byte(`{"type":"integer","minimum":0,"maximum":100}`)
		return validPayloadTestCase{Schema: schema, Payload: float64(n)}
	})
}

func genValidObjectPayload() gopter.Gen {
	return gen.AlphaString().
		SuchThat(func(s string) bool { return len(s) >= 1 }).
		Map(func(name string) validPayloadTestCase {
			schema := []byte(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
			payload := map[string]any{"name": name}
			return validPayloadTestCase{Schema: schema, Payload: payload}
		})
}

func genValidArrayPayload() gopter.Gen {
	return gen.SliceOfN(3, gen.AlphaString()).Map(func(items []string) validPayloadTestCase {
		schema := []byte(`{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":10}`)
		payload := make([]any, len(items))
		for i, item := range items {
			payload[i] = item
		}
		return validPayloadTestCase{Schema: schema, Payload: payload}
	})
}

func genValidBooleanPayload() gopter.Gen {
	return gen.Bool().Map(func(b bool) validPayloadTestCase {
		schema := []byte(`{"type":"boolean"}`)
		return validPayloadTestCase{Schema: schema, Payload: b}
	})
}

func genMissingRequiredFieldTestCase() gopter.Gen {
	return gen.OneConstOf(
		missingRequiredFieldTestCase{
			Schema:  []byte(`{"type":"object","properties":{"name":{"type":"string"},"age":{"type":"integer"}},"required":["name","age"]}`),
			Payload: map[string]any{"name": "test"},
		},
		missingRequiredFieldTestCase{
			Schema:  []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
			Payload: map[string]any{},
		},
		missingRequiredFieldTestCase{
			Schema:  []byte(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"},"c":{"type":"string"}},"required":["a","b","c"]}`),
			Payload: map[string]any{"a": "x", "c": "z"},
		},
	)
}

func genWrongTypeTestCase() gopter.Gen {
	return gen.OneConstOf(
		wrongTypeTestCase{
			Schema:  []byte(`{"type":"string"}`),
			Payload: 123,
		},
		wrongTypeTestCase{
			Schema:  []byte(`{"type":"integer"}`),
			Payload: "not a number",
		},
		wrongTypeTestCase{
			Schema:  []byte(`{"type":"boolean"}`),
			Payload: "true",
		},
		wrongTypeTestCase{
			Schema:  []byte(`{"type":"array","items":{"type":"string"}}`),
			Payload: "not an array",
		},
		wrongTypeTestCase{
			Schema:  []byte(`{"type":"object","properties":{"name":{"type":"string"}}}`),
			Payload: []any{"not", "an", "object"},
		},
		wrongTypeTestCase{
			Schema:  []byte(`{"type":"object","properties":{"count":{"type":"integer"}}}`),
			Payload: map[string]any{"count": "not an integer"},
		},
	)
}

func genConstraintViolationTestCase() gopter.Gen {
	return gen.OneConstOf(
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"string","minLength":5}`),
			Payload: "abc",
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"string","maxLength":3}`),
			Payload: "toolong",
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"integer","minimum":10}`),
			Payload: float64(5),
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"integer","maximum":10}`),
			Payload: float64(15),
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"array","minItems":2}`),
			Payload: []any{"one"},
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"array","maxItems":2}`),
			Payload: []any{"one", "two", "three"},
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"string","enum":["a","b","c"]}`),
			Payload: "d",
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"number","exclusiveMinimum":10}`),
			Payload: float64(10),
		},
		constraintViolationTestCase{
			Schema:  []byte(`{"type":"number","exclusiveMaximum":10}`),
			Payload: float64(10),
		},
	)
}
