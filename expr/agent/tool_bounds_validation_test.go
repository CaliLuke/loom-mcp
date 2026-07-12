package agent

import (
	"testing"

	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolExprValidatePagingRejectsInvalidArgs(t *testing.T) {
	cases := []struct {
		name   string
		args   *goaexpr.AttributeExpr
		paging *ToolPagingExpr
		want   string
	}{
		{
			name:   "missing cursor declaration",
			paging: &ToolPagingExpr{NextCursorField: "next_cursor"},
			want:   "Cursor() is required when configuring paging",
		},
		{
			name:   "missing next cursor declaration",
			paging: &ToolPagingExpr{CursorField: "cursor"},
			want:   "NextCursor() is required when configuring paging",
		},
		{
			name:   "empty Args",
			paging: validPaging(),
			want:   "Args must be non-empty when configuring paging",
		},
		{
			name:   "missing cursor field",
			args:   validationObject(validationField("query", goaexpr.String)),
			paging: validPaging(),
			want:   `Args must define an optional String field named "cursor" when configuring paging`,
		},
		{
			name:   "cursor field has wrong type",
			args:   validationObject(validationField("cursor", goaexpr.Int)),
			paging: validPaging(),
			want:   `Args field "cursor" must be a String when configuring paging`,
		},
		{
			name:   "cursor field is required",
			args:   validationRequiredObject([]string{"cursor"}, validationField("cursor", goaexpr.String)),
			paging: validPaging(),
			want:   `Args field "cursor" must be optional when configuring paging`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := &ToolExpr{
				Name:   "search",
				Args:   tc.args,
				Bounds: &ToolBoundsExpr{Paging: tc.paging},
			}

			assert.ErrorContains(t, tool.Validate(), tc.want)
		})
	}
}

func TestToolExprValidatePagingAcceptsOptionalStringCursor(t *testing.T) {
	tool := &ToolExpr{
		Name: "search",
		Args: validationObject(
			validationField("query", goaexpr.String),
			validationField("cursor", goaexpr.String),
		),
		Bounds: &ToolBoundsExpr{Paging: validPaging()},
	}

	require.NoError(t, tool.Validate())
}

func TestToolExprValidateBoundsRejectsCanonicalToolReturnFields(t *testing.T) {
	for _, field := range []string{"returned", "truncated", "total", "refinement_hint", "next_cursor"} {
		t.Run(field, func(t *testing.T) {
			tool := &ToolExpr{
				Name: "search",
				Args: validationObject(validationField("cursor", goaexpr.String)),
				Return: validationObject(
					validationField(field, goaexpr.String),
				),
				Bounds: &ToolBoundsExpr{Paging: validPaging()},
			}

			assert.ErrorContains(t, tool.Validate(), "bounded tool return must not define canonical bounds field")
		})
	}
}

func TestToolExprValidateBoundedMethodResult(t *testing.T) {
	cases := []struct {
		name   string
		result *goaexpr.AttributeExpr
		want   string
	}{
		{
			name: "nil result",
			want: "bounded method result requires a non-empty bound method result",
		},
		{
			name:   "missing returned",
			result: validBoundedMethodResultWithout("returned"),
			want:   `bounded method result must define "returned" on the bound method result`,
		},
		{
			name:   "missing truncated",
			result: validBoundedMethodResultWithout("truncated"),
			want:   `bounded method result must define "truncated" on the bound method result`,
		},
		{
			name: "returned has wrong type",
			result: validationRequiredObject(
				[]string{"returned", "truncated"},
				validationField("returned", goaexpr.String),
				validationField("truncated", goaexpr.Boolean),
				validationField("next_cursor", goaexpr.String),
			),
			want: `bounded method result field "returned" must be a Int`,
		},
		{
			name: "returned must be required",
			result: validationRequiredObject(
				[]string{"truncated"},
				validationField("returned", goaexpr.Int),
				validationField("truncated", goaexpr.Boolean),
				validationField("next_cursor", goaexpr.String),
			),
			want: `bounded method result field "returned" must be required`,
		},
		{
			name: "optional total must have correct type",
			result: validationRequiredObject(
				[]string{"returned", "truncated"},
				validationField("returned", goaexpr.Int),
				validationField("truncated", goaexpr.Boolean),
				validationField("total", goaexpr.String),
				validationField("next_cursor", goaexpr.String),
			),
			want: `bounded method result field "total" must be a Int`,
		},
		{
			name: "total must be optional",
			result: validationRequiredObject(
				[]string{"returned", "truncated", "total"},
				validationField("returned", goaexpr.Int),
				validationField("truncated", goaexpr.Boolean),
				validationField("total", goaexpr.Int),
				validationField("next_cursor", goaexpr.String),
			),
			want: `bounded method result field "total" must be optional`,
		},
		{
			name:   "missing next cursor",
			result: validBoundedMethodResultWithout("next_cursor"),
			want:   `bounded method result must define "next_cursor" on the bound method result`,
		},
		{
			name: "next cursor must be optional",
			result: validationRequiredObject(
				[]string{"returned", "truncated", "next_cursor"},
				validationField("returned", goaexpr.Int),
				validationField("truncated", goaexpr.Boolean),
				validationField("next_cursor", goaexpr.String),
			),
			want: `bounded method result field "next_cursor" must be optional`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preserveGlobalRoots(t)
			tool := boundPagingTool(tc.result)
			assert.ErrorContains(t, tool.Validate(), tc.want)
		})
	}
}

func TestToolExprValidateBoundedMethodResultAcceptsContract(t *testing.T) {
	preserveGlobalRoots(t)
	tool := boundPagingTool(validBoundedMethodResultWithout(""))

	require.NoError(t, tool.Validate())
}

func validPaging() *ToolPagingExpr {
	return &ToolPagingExpr{CursorField: "cursor", NextCursorField: "next_cursor"}
}

func validBoundedMethodResultWithout(omitted string) *goaexpr.AttributeExpr {
	fields := []validationTestField{
		validationField("returned", goaexpr.Int),
		validationField("truncated", goaexpr.Boolean),
		validationField("total", goaexpr.Int),
		validationField("refinement_hint", goaexpr.String),
		validationField("next_cursor", goaexpr.String),
	}
	filtered := make([]validationTestField, 0, len(fields))
	required := []string{"returned", "truncated"}
	for _, field := range fields {
		if field.name != omitted {
			filtered = append(filtered, field)
		}
	}
	return validationRequiredObject(required, filtered...)
}

func boundPagingTool(result *goaexpr.AttributeExpr) *ToolExpr {
	service := &goaexpr.ServiceExpr{Name: "svc"}
	method := &goaexpr.MethodExpr{Name: "Search", Service: service, Result: result}
	service.Methods = []*goaexpr.MethodExpr{method}
	goaexpr.Root = &goaexpr.RootExpr{Services: []*goaexpr.ServiceExpr{service}}

	agent := &AgentExpr{Name: "agent", Service: service}
	tool := &ToolExpr{
		Name:    "search",
		Toolset: &ToolsetExpr{Name: "tools", Agent: agent},
		Args:    validationObject(validationField("cursor", goaexpr.String)),
		Bounds:  &ToolBoundsExpr{Paging: validPaging()},
	}
	tool.RecordBinding("svc", "Search")
	return tool
}
