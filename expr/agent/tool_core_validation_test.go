package agent

import (
	"testing"

	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolExprValidateRejectsUnresolvedBindings(t *testing.T) {
	t.Run("service", func(t *testing.T) {
		preserveGlobalRoots(t)
		goaexpr.Root = &goaexpr.RootExpr{}
		tool := &ToolExpr{Name: "lookup"}
		tool.RecordBinding("missing", "Lookup")

		assert.ErrorContains(t, tool.Validate(), "BindTo could not resolve target service")
	})

	t.Run("method", func(t *testing.T) {
		preserveGlobalRoots(t)
		service := &goaexpr.ServiceExpr{Name: "svc"}
		goaexpr.Root = &goaexpr.RootExpr{Services: []*goaexpr.ServiceExpr{service}}
		tool := &ToolExpr{Name: "lookup"}
		tool.RecordBinding("svc", "Missing")

		assert.ErrorContains(t, tool.Validate(), "service method \"Missing\" not found in service \"svc\"")
	})
}

func TestToolExprValidateRejectsUnsupportedShapes(t *testing.T) {
	cases := []struct {
		name string
		tool *ToolExpr
		want string
	}{
		{
			name: "Args",
			tool: &ToolExpr{Name: "lookup", Args: &goaexpr.AttributeExpr{Type: invalidToolDataType{}}},
			want: "Args must be a user type, primitive, or composite shape",
		},
		{
			name: "Return",
			tool: &ToolExpr{Name: "lookup", Return: &goaexpr.AttributeExpr{Type: invalidToolDataType{}}},
			want: "Return must be a user type, primitive, or composite shape",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorContains(t, tc.tool.Validate(), tc.want)
		})
	}
}

func TestToolExprFinalizeIsIdempotent(t *testing.T) {
	preserveGlobalRoots(t)
	service := &goaexpr.ServiceExpr{Name: "svc"}
	method := &goaexpr.MethodExpr{Name: "Lookup", Service: service}
	service.Methods = []*goaexpr.MethodExpr{method}
	goaexpr.Root = &goaexpr.RootExpr{Services: []*goaexpr.ServiceExpr{service}}
	agent := &AgentExpr{Name: "agent", Service: service}
	tool := &ToolExpr{
		Name:    "lookup",
		Toolset: &ToolsetExpr{Name: "tools", Agent: agent},
		Args:    validationObject(validationField("query", goaexpr.String)),
		Return:  validationObject(validationField("answer", goaexpr.String)),
	}
	tool.RecordBinding("svc", "Lookup")
	require.NoError(t, tool.Validate())

	tool.Finalize()
	firstMethod := tool.Method
	firstArgs := tool.Args.Type.Hash()
	firstReturn := tool.Return.Type.Hash()
	tool.Finalize()

	assert.Same(t, firstMethod, tool.Method)
	assert.Equal(t, firstArgs, tool.Args.Type.Hash())
	assert.Equal(t, firstReturn, tool.Return.Type.Hash())
}

type invalidToolDataType struct{}

func (invalidToolDataType) Kind() goaexpr.Kind {
	return goaexpr.ObjectKind
}

func (invalidToolDataType) Name() string {
	return "invalid"
}

func (invalidToolDataType) IsCompatible(any) bool {
	return false
}

func (invalidToolDataType) Example(*goaexpr.ExampleGenerator) any {
	return nil
}

func (invalidToolDataType) Hash() string {
	return "invalid"
}
