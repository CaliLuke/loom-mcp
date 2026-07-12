package agent

import (
	"testing"

	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolExprValidateServerDataRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name       string
		serverData []*ServerDataExpr
		want       string
	}{
		{
			name: "empty kind",
			serverData: []*ServerDataExpr{
				{Schema: &goaexpr.AttributeExpr{Type: goaexpr.String}},
			},
			want: "ServerData kind must be non-empty",
		},
		{
			name: "missing schema",
			serverData: []*ServerDataExpr{
				{Kind: "evidence"},
			},
			want: `ServerData("evidence") must declare a schema type`,
		},
		{
			name: "source requires binding",
			serverData: []*ServerDataExpr{
				{
					Kind:   "evidence",
					Schema: &goaexpr.AttributeExpr{Type: goaexpr.String},
					Source: &ServerDataSourceExpr{MethodResultField: "Evidence"},
				},
			},
			want: `ServerData("evidence") with FromMethodResultField requires a bound method (BindTo)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := &ToolExpr{Name: "lookup", ServerData: tc.serverData}
			assert.ErrorContains(t, tool.Validate(), tc.want)
		})
	}
}

func TestToolExprValidateServerDataRejectsMissingBoundResultField(t *testing.T) {
	preserveGlobalRoots(t)
	result := validationObject(validationField("Other", goaexpr.String))
	tool := boundServerDataTool(result, &ServerDataExpr{
		Kind:   "evidence",
		Schema: &goaexpr.AttributeExpr{Type: goaexpr.String},
		Source: &ServerDataSourceExpr{MethodResultField: "Evidence"},
	})

	assert.ErrorContains(t, tool.Validate(), `ServerData("evidence") FromMethodResultField("Evidence") does not exist on method result`)
}

func TestToolExprValidateServerDataRejectsNilBoundMethodResult(t *testing.T) {
	preserveGlobalRoots(t)
	tool := boundServerDataTool(nil, &ServerDataExpr{
		Kind:   "evidence",
		Schema: &goaexpr.AttributeExpr{Type: goaexpr.String},
		Source: &ServerDataSourceExpr{MethodResultField: "Evidence"},
	})

	var err error
	require.NotPanics(t, func() {
		err = tool.Validate()
	})
	assert.ErrorContains(t, err, `ServerData("evidence") FromMethodResultField("Evidence") does not exist on method result`)
}

func TestToolExprValidateServerDataAcceptsValidDeclarations(t *testing.T) {
	t.Run("unbound", func(t *testing.T) {
		tool := &ToolExpr{
			Name: "lookup",
			ServerData: []*ServerDataExpr{
				nil,
				{Kind: "evidence", Schema: &goaexpr.AttributeExpr{Type: goaexpr.String}},
			},
		}

		require.NoError(t, tool.Validate())
	})

	t.Run("bound result field", func(t *testing.T) {
		preserveGlobalRoots(t)
		result := validationObject(validationField("Evidence", goaexpr.String))
		tool := boundServerDataTool(result, &ServerDataExpr{
			Kind:   "evidence",
			Schema: &goaexpr.AttributeExpr{Type: goaexpr.String},
			Source: &ServerDataSourceExpr{MethodResultField: "Evidence"},
		})

		require.NoError(t, tool.Validate())
	})
}

type validationTestField struct {
	name string
	typ  goaexpr.DataType
}

func validationField(name string, typ goaexpr.DataType) validationTestField {
	return validationTestField{name: name, typ: typ}
}

func validationObject(fields ...validationTestField) *goaexpr.AttributeExpr {
	return validationRequiredObject(nil, fields...)
}

func validationRequiredObject(required []string, fields ...validationTestField) *goaexpr.AttributeExpr {
	obj := make(goaexpr.Object, 0, len(fields))
	for _, field := range fields {
		obj = append(obj, &goaexpr.NamedAttributeExpr{
			Name:      field.name,
			Attribute: &goaexpr.AttributeExpr{Type: field.typ},
		})
	}
	return &goaexpr.AttributeExpr{
		Type:       &obj,
		Validation: &goaexpr.ValidationExpr{Required: required},
	}
}

func boundServerDataTool(result *goaexpr.AttributeExpr, serverData ...*ServerDataExpr) *ToolExpr {
	service := &goaexpr.ServiceExpr{Name: "svc"}
	method := &goaexpr.MethodExpr{Name: "Lookup", Service: service, Result: result}
	service.Methods = []*goaexpr.MethodExpr{method}
	goaexpr.Root = &goaexpr.RootExpr{Services: []*goaexpr.ServiceExpr{service}}

	agent := &AgentExpr{Name: "agent", Service: service}
	tool := &ToolExpr{
		Name:       "lookup",
		Toolset:    &ToolsetExpr{Name: "tools", Agent: agent},
		ServerData: serverData,
	}
	tool.RecordBinding("svc", "Lookup")
	return tool
}
