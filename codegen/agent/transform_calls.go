package codegen

import "github.com/dave/jennifer/jen"

// toolTransformAssignment preserves infallible signatures and checks fallible
// conversions before the caller can dispatch or publish their values.
func toolTransformAssignment(tool *ToolData, target, operator, name string, input *jen.Statement, failure jen.Code) *jen.Statement {
	statement := jen.Id(target)
	if tool.TransformErrors[name] {
		statement = jen.List(statement, jen.Id("err"))
	}
	statement.Op(operator).Id(name).Call(input)
	if tool.TransformErrors[name] {
		statement.Line().If(jen.Id("err").Op("!=").Nil()).Block(failure)
	}
	return statement
}

func toolProviderConversionError(payload bool) *jen.Statement {
	code, message := "encode_failed", "tool execution failed"
	if payload {
		code, message = "invalid_arguments", "tool arguments failed validation"
	}
	return jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Id("msg").Dot("RegistrationToken"), jen.Id("msg").Dot("ToolUseID"), jen.Lit(code), jen.Lit(message)), jen.Nil())
}
