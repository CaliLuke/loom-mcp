package codegen

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CaliLuke/loom/expr"
)

func TestCollectTopLevelValidations(t *testing.T) {
	t.Parallel()

	attr := &expr.AttributeExpr{
		Type: &expr.Object{
			{
				Name: "status",
				Attribute: &expr.AttributeExpr{
					Type: expr.String,
					Validation: &expr.ValidationExpr{
						Values: []any{"open", "closed"},
					},
				},
			},
			{
				Name: "workflow_id",
				Attribute: &expr.AttributeExpr{
					Type:         expr.String,
					DefaultValue: "prd-generation",
					Validation: &expr.ValidationExpr{
						Values: []any{"prd-generation", "technical-design"},
					},
				},
			},
			{
				Name: "limit",
				Attribute: &expr.AttributeExpr{
					Type: expr.Int,
				},
			},
		},
		Validation: &expr.ValidationExpr{
			Required: []string{"status", "limit"},
		},
	}

	required, enums, defaults := collectTopLevelValidations(attr)

	assert.Equal(t, []string{"status"}, required)
	if assert.Len(t, enums, 2) {
		assert.Equal(t, EnumField{Name: "status", Values: []string{"open", "closed"}, Pointer: false}, enums[0])
		assert.Equal(t, EnumField{Name: "workflow_id", Values: []string{"prd-generation", "technical-design"}, Pointer: false}, enums[1])
	}
	if assert.Len(t, defaults, 1) {
		assert.Equal(t, DefaultField{
			Name:    "workflow_id",
			GoName:  "WorkflowID",
			Literal: `"prd-generation"`,
			Kind:    "string",
		}, defaults[0])
	}
}

func TestCollectTopLevelValidationsPreservesEnumDeclarationOrder(t *testing.T) {
	t.Parallel()

	attr := &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "second", Attribute: &expr.AttributeExpr{Type: expr.String, Validation: &expr.ValidationExpr{Values: []any{"b"}}}},
			{Name: "first", Attribute: &expr.AttributeExpr{Type: expr.String, Validation: &expr.ValidationExpr{Values: []any{"a"}}}},
		},
	}

	_, enums, _ := collectTopLevelValidations(attr)

	assert.Equal(t, []EnumField{
		{Name: "second", Values: []string{"b"}, Pointer: true},
		{Name: "first", Values: []string{"a"}, Pointer: true},
	}, enums)
}
