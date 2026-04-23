// Package shared provides common utilities for code generation across protocols.
package shared

import "github.com/CaliLuke/loom/expr"

// ToJSONSchema returns a compact JSON Schema for the given Loom attribute.
// It delegates to Loom's inline schema builder so MCP contracts share the same
// wire-tag, wrapper, and recursion semantics as the framework.
func ToJSONSchema(attr *expr.AttributeExpr) (string, error) {
	schema, err := expr.InlineJSONSchema(attr)
	if err != nil {
		return "", err
	}
	return string(schema), nil
}
