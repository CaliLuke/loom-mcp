package jsontransport

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/uniongen"
	"github.com/dave/jennifer/jen"
)

// UnionsSection delegates JSON union methods and matching to Loom.
func UnionsSection(unions []*UnionType) codegen.Section {
	return &codegen.JenniferSection{Name: "tool-spec-union-types", Build: func(stmt *jen.Statement) {
		for _, union := range unions {
			uniongen.Emit(stmt, &union.Type)
			for _, validation := range union.Validators {
				stmt.Add(codegen.Expr(validation)).Line()
			}
		}
	}}
}
