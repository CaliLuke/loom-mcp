package codegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom/expr"
)

func TestBuildResourceQueryFields(t *testing.T) {
	t.Parallel()

	payload := &expr.AttributeExpr{
		Type: &expr.Object{
			{
				Name: "query",
				Attribute: &expr.AttributeExpr{
					Type: expr.String,
				},
			},
			{
				Name: "include_archived",
				Attribute: &expr.AttributeExpr{
					Type: expr.Boolean,
				},
			},
			{
				Name: "tags",
				Attribute: &expr.AttributeExpr{
					Type: &expr.Array{ElemType: &expr.AttributeExpr{Type: expr.String}},
				},
			},
		},
	}

	fields, err := buildResourceQueryFields(payload)
	require.NoError(t, err)
	require.Len(t, fields, 3)

	assert.Equal(t, &ResourceQueryField{
		QueryKey:   "include_archived",
		GuardExpr:  "payload.IncludeArchived != nil",
		ValueExpr:  "*payload.IncludeArchived",
		FormatKind: resourceQueryFormatBool,
	}, fields[0])
	assert.Equal(t, &ResourceQueryField{
		QueryKey:   "query",
		GuardExpr:  "payload.Query != nil",
		ValueExpr:  "*payload.Query",
		FormatKind: resourceQueryFormatString,
	}, fields[1])
	assert.Equal(t, &ResourceQueryField{
		QueryKey:       "tags",
		GuardExpr:      "len(payload.Tags) > 0",
		ValueExpr:      "value",
		CollectionExpr: "payload.Tags",
		FormatKind:     resourceQueryFormatString,
		Repeated:       true,
	}, fields[2])
}

func TestBuildResourceQueryFields_RejectsNestedArray(t *testing.T) {
	t.Parallel()

	payload := &expr.AttributeExpr{
		Type: &expr.Object{
			{
				Name: "bad",
				Attribute: &expr.AttributeExpr{
					Type: &expr.Array{
						ElemType: &expr.AttributeExpr{
							Type: &expr.Array{ElemType: &expr.AttributeExpr{Type: expr.String}},
						},
					},
				},
			},
		},
	}

	fields, err := buildResourceQueryFields(payload)
	require.Nil(t, fields)
	require.ErrorContains(t, err, "nested array query values")
}
