package codegen

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaPreservesIntegerBounds(t *testing.T) {
	for _, tt := range []struct {
		name    string
		kind    goaexpr.DataType
		minimum string
		maximum string
	}{
		{"Int", goaexpr.Int, "-9223372036854775808", "9223372036854775807"},
		{"Int64", goaexpr.Int64, "-9223372036854775808", "9223372036854775807"},
		{"UInt64", goaexpr.UInt64, "0", "18446744073709551615"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attribute := &goaexpr.AttributeExpr{Type: &goaexpr.Object{
				&goaexpr.NamedAttributeExpr{Name: "value", Attribute: &goaexpr.AttributeExpr{Type: tt.kind}},
			}}
			schema, err := schemaForAttribute(attribute)
			require.NoError(t, err)
			for _, bounded := range []bool{false, true} {
				if bounded {
					schema, err = projectBoundedResultSchema(schema, &ToolBoundsData{})
					require.NoError(t, err)
				}
				var document struct {
					Properties map[string]map[string]jsontext.Value `json:"properties"`
				}
				require.NoError(t, json.Unmarshal(schema, &document))
				assert.Equal(t, tt.minimum, string(document.Properties["value"]["minimum"]), "bounded=%v", bounded)
				assert.Equal(t, tt.maximum, string(document.Properties["value"]["maximum"]), "bounded=%v", bounded)
			}
		})
	}
}
