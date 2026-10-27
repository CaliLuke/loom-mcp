package codegen

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/codegen/shared"
	"github.com/CaliLuke/loom/expr"
)

// toolInputSchema adds MCP's object envelope to flat, object-only unions.
// The original oneOf branches remain the authority for input validation.
func toolInputSchema(payload *expr.AttributeExpr) (string, error) {
	schema, err := shared.ToJSONSchema(payload)
	if err != nil {
		return "", err
	}
	union := expr.AsUnion(payload.Type)
	if union == nil || !union.Untagged || expr.AllowsNull(payload) {
		return schema, nil
	}
	for _, branch := range union.Values {
		if expr.AsObject(branch.Attribute.Type) == nil || expr.AllowsNull(branch.Attribute) {
			return schema, nil
		}
	}
	return objectUnionInputSchema(schema)
}

// objectUnionInputSchema projects a property catalogue without narrowing any
// branch. An open branch contributes its additional-property schema for fields
// that it does not declare; a closed branch contributes no possible value.
func objectUnionInputSchema(schema string) (string, error) {
	var root map[string]jsontext.Value
	if err := json.Unmarshal([]byte(schema), &root); err != nil {
		return "", fmt.Errorf("decode object union schema: %w", err)
	}
	var branches []map[string]jsontext.Value
	if err := json.Unmarshal(root["oneOf"], &branches); err != nil {
		return "", fmt.Errorf("decode object union branches: %w", err)
	}
	branchProperties := make([]map[string]jsontext.Value, len(branches))
	properties := make(map[string]jsontext.Value)
	for i, branch := range branches {
		if raw := branch["properties"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &branchProperties[i]); err != nil {
				return "", fmt.Errorf("decode object union properties: %w", err)
			}
		}
		for name := range branchProperties[i] {
			properties[name] = nil
		}
	}
	for name := range properties {
		var alternatives []jsontext.Value
		for i, branch := range branches {
			value, exists := branchProperties[i][name]
			if !exists {
				value = branch["additionalProperties"]
				if bytes.Equal(value, []byte("false")) {
					continue
				}
				if len(value) == 0 || bytes.Equal(value, []byte("true")) {
					value = jsontext.Value(`{}`)
				}
			}
			if !slices.ContainsFunc(alternatives, func(other jsontext.Value) bool {
				return bytes.Equal(value, other)
			}) {
				alternatives = append(alternatives, value)
			}
		}
		if len(alternatives) == 1 {
			properties[name] = alternatives[0]
			continue
		}
		encoded, err := json.Marshal(map[string][]jsontext.Value{"anyOf": alternatives}, json.Deterministic(true), jsontext.CanonicalizeRawInts(false))
		if err != nil {
			return "", fmt.Errorf("encode union property %q: %w", name, err)
		}
		properties[name] = encoded
	}
	encoded, err := json.Marshal(properties, json.Deterministic(true), jsontext.CanonicalizeRawInts(false))
	if err != nil {
		return "", fmt.Errorf("encode union properties: %w", err)
	}
	root["type"] = jsontext.Value(`"object"`)
	root["properties"] = encoded
	encoded, err = json.Marshal(root, json.Deterministic(true), jsontext.CanonicalizeRawInts(false))
	if err != nil {
		return "", fmt.Errorf("encode object union schema: %w", err)
	}
	return string(encoded), nil
}
