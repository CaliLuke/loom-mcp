package codegen

import (
	"testing"

	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestSchemaForAttributePropagatesInlineSchemaError(t *testing.T) {
	recursive := &goaexpr.UserTypeExpr{TypeName: "Node"}
	object := goaexpr.Object{
		&goaexpr.NamedAttributeExpr{
			Name: "next",
			Attribute: &goaexpr.AttributeExpr{
				Type: recursive,
			},
		},
	}
	recursive.AttributeExpr = &goaexpr.AttributeExpr{Type: &object}

	_, err := schemaForAttribute(&goaexpr.AttributeExpr{Type: recursive})

	require.Error(t, err)
	require.ErrorContains(t, err, "recursive user type")
}
