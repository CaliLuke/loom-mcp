package agent

import (
	"testing"

	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolExprValidateInjectRejectsInvalidUnboundPayloads(t *testing.T) {
	cases := []struct {
		name     string
		args     *goaexpr.AttributeExpr
		injected []string
		want     string
	}{
		{
			name:     "empty field name",
			args:     injectObject(injectField("session_id", goaexpr.String), "session_id"),
			injected: []string{""},
			want:     "Inject requires non-empty field names",
		},
		{
			name:     "duplicate field name",
			args:     injectObject(injectField("session_id", goaexpr.String), "session_id"),
			injected: []string{"session_id", "session_id"},
			want:     `Inject field "session_id" is declared more than once`,
		},
		{
			name:     "empty payload",
			injected: []string{"session_id"},
			want:     "Inject requires a non-empty tool payload",
		},
		{
			name:     "non-object payload",
			args:     &goaexpr.AttributeExpr{Type: goaexpr.String},
			injected: []string{"session_id"},
			want:     "Inject requires the tool payload to be an object",
		},
		{
			name:     "missing field",
			args:     injectObject(injectField("other", goaexpr.String), "other"),
			injected: []string{"session_id"},
			want:     `Inject field "session_id" does not exist on the tool payload`,
		},
		{
			name:     "optional field",
			args:     injectObject(injectField("session_id", goaexpr.String)),
			injected: []string{"session_id"},
			want:     `Inject field "session_id" must be required on the tool payload`,
		},
		{
			name:     "non-string field",
			args:     injectObject(injectField("session_id", goaexpr.Int), "session_id"),
			injected: []string{"session_id"},
			want:     `Inject field "session_id" must be a String on the tool payload`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := &ToolExpr{
				Name:           "lookup",
				Args:           tc.args,
				InjectedFields: tc.injected,
			}

			assert.ErrorContains(t, tool.Validate(), tc.want)
		})
	}
}

func TestToolExprValidateInjectAcceptsRequiredString(t *testing.T) {
	tool := &ToolExpr{
		Name:           "lookup",
		Args:           injectObject(injectField("tenant_id", goaexpr.String), "tenant_id"),
		InjectedFields: []string{"tenant_id"},
	}

	require.NoError(t, tool.Validate())
}

func TestToolExprValidateInjectRejectsDivergentBoundPayloads(t *testing.T) {
	cases := []struct {
		name          string
		toolArgs      *goaexpr.AttributeExpr
		methodPayload *goaexpr.AttributeExpr
		want          string
	}{
		{
			name:          "field only on tool Args",
			toolArgs:      injectObject(injectField("session_id", goaexpr.String), "session_id"),
			methodPayload: injectObject(injectField("other", goaexpr.String), "other"),
			want:          `Inject field "session_id" does not exist on the bound method payload even though the tool Args defines it`,
		},
		{
			name:          "field only on method payload",
			toolArgs:      injectObject(injectField("other", goaexpr.String), "other"),
			methodPayload: injectObject(injectField("session_id", goaexpr.String), "session_id"),
			want:          `Inject field "session_id" does not exist on the tool Args even though the bound method payload defines it`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preserveGlobalRoots(t)
			tool := boundInjectTool(tc.toolArgs, tc.methodPayload, "session_id")

			assert.ErrorContains(t, tool.Validate(), tc.want)
		})
	}
}

func TestToolExprValidateInjectRejectsLabelBackedBoundField(t *testing.T) {
	preserveGlobalRoots(t)
	args := injectObject(injectField("tenant_id", goaexpr.String), "tenant_id")
	tool := boundInjectTool(args, args, "tenant_id")

	assert.ErrorContains(t, tool.Validate(), `Inject field "tenant_id" is label-backed`)
}

func TestToolExprValidateInjectAcceptsRuntimeMetaBoundField(t *testing.T) {
	preserveGlobalRoots(t)
	args := injectObject(injectField("session_id", goaexpr.String), "session_id")
	tool := boundInjectTool(args, args, "session_id")

	require.NoError(t, tool.Validate())
	require.NotNil(t, tool.Method)
}

type injectTestField struct {
	name string
	typ  goaexpr.DataType
}

func injectField(name string, typ goaexpr.DataType) injectTestField {
	return injectTestField{name: name, typ: typ}
}

func injectObject(field injectTestField, required ...string) *goaexpr.AttributeExpr {
	obj := &goaexpr.Object{
		&goaexpr.NamedAttributeExpr{
			Name:      field.name,
			Attribute: &goaexpr.AttributeExpr{Type: field.typ},
		},
	}
	return &goaexpr.AttributeExpr{
		Type:       obj,
		Validation: &goaexpr.ValidationExpr{Required: required},
	}
}

func boundInjectTool(toolArgs, methodPayload *goaexpr.AttributeExpr, injected ...string) *ToolExpr {
	service := &goaexpr.ServiceExpr{Name: "svc"}
	method := &goaexpr.MethodExpr{Name: "Lookup", Service: service, Payload: methodPayload}
	service.Methods = []*goaexpr.MethodExpr{method}
	goaexpr.Root = &goaexpr.RootExpr{Services: []*goaexpr.ServiceExpr{service}}

	agent := &AgentExpr{Name: "agent", Service: service}
	toolset := &ToolsetExpr{Name: "tools", Agent: agent}
	tool := &ToolExpr{
		Name:           "lookup",
		Args:           toolArgs,
		InjectedFields: injected,
		Toolset:        toolset,
	}
	tool.RecordBinding("svc", "Lookup")
	return tool
}
