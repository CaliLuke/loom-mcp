package codegen

import (
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestGeneratePayloadTypeNameCollisions(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()
	svc, methods := testService("collision", "lookup")
	method := methods["lookup"]
	method.Prepare()
	method.StreamingResult = nil
	first := &expr.UserTypeExpr{TypeName: "foo_bar", AttributeExpr: &expr.AttributeExpr{Type: &expr.Object{
		{Name: "weight", Attribute: &expr.AttributeExpr{Type: expr.Float64, DefaultValue: 1.5}},
	}}}
	second := &expr.UserTypeExpr{TypeName: "fooBar", AttributeExpr: &expr.AttributeExpr{Type: &expr.Object{
		{Name: "label", Attribute: &expr.AttributeExpr{Type: expr.String, DefaultValue: "standard"}},
	}}}
	method.Payload = &expr.AttributeExpr{Type: &expr.UserTypeExpr{TypeName: "LookupPayload", AttributeExpr: &expr.AttributeExpr{Type: &expr.Object{
		{Name: "first", Attribute: &expr.AttributeExpr{Type: first}},
		{Name: "second", Attribute: &expr.AttributeExpr{Type: second}},
	}}}}
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{Name: "collision", Version: "1.0.0", Tools: []*mcpexpr.ToolExpr{{Name: "lookup", Method: method}}})
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	root.Types = []expr.UserType{first, second}
	files, err := Generate("example.com/collision/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	compileMetadataModule(t, "example.com/collision", root, files)
}
