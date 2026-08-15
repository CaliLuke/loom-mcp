package codegen

import "github.com/dave/jennifer/jen"

func rawExpr(expr string) jen.Code {
	return jen.Op(expr)
}
