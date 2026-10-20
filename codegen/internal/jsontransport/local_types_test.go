package jsontransport

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/CaliLuke/loom/codegen"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestLocalizeNestedTypesPreservesDistinctNames(t *testing.T) {
	root := expr.RunDSL(t, func() {
		first := Type("foo_bar", func() {
			Attribute("text", String)
		})
		second := Type("fooBar", func() {
			Attribute("count", Int)
		})
		Service("collision", func() {
			Method("lookup", func() {
				Payload(func() {
					Attribute("first", first)
					Attribute("second", second)
					Attribute("again", first)
				})
			})
		})
	})
	payload := expr.AsObject(root.Services[0].Methods[0].Payload.Type)
	transport, locals, err := LocalizeNestedTypes(CloneWithJSONTags(&expr.AttributeExpr{Type: payload}))
	require.NoError(t, err)
	require.Len(t, locals, 2)
	scope := codegen.NewNameScope()
	obj := expr.AsObject(transport.Type)
	firstRef := scope.GoTypeRef(obj.Attribute("first"))
	secondRef := scope.GoTypeRef(obj.Attribute("second"))
	require.NotEqual(t, firstRef, secondRef)
	require.Equal(t, firstRef, scope.GoTypeRef(obj.Attribute("again")))
	source := "package generated\ntype Payload " + scope.GoTypeDef(transport, true, false) + "\n"
	for _, local := range locals {
		source += "type " + scope.GoTypeName(&expr.AttributeExpr{Type: local}) + " " + scope.GoTypeDef(local.Attribute(), true, false) + "\n"
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "payload.go", source, parser.AllErrors)
	require.NoError(t, err)
	config := &types.Config{}
	_, err = config.Check("generated", fset, []*ast.File{file}, nil)
	require.NoError(t, err, source)
}
