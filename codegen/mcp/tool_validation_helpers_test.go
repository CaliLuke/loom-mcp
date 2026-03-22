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

	required, enums, enumPtr, defaults := collectTopLevelValidations(attr)

	assert.Equal(t, []string{"status"}, required)
	assert.Equal(t, []string{"open", "closed"}, enums["status"])
	assert.Equal(t, []string{"prd-generation", "technical-design"}, enums["workflow_id"])
	assert.False(t, enumPtr["status"])
	assert.False(t, enumPtr["workflow_id"])
	if assert.Len(t, defaults, 1) {
		assert.Equal(t, DefaultField{
			Name:    "workflow_id",
			GoName:  "WorkflowID",
			Literal: `"prd-generation"`,
			Kind:    "string",
		}, defaults[0])
	}
}
