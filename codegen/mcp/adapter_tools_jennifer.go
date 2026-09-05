package codegen

import (
	"fmt"

	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func adapterToolsSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-tools", func(stmt *jen.Statement) {
		stmt.Comment("Tools handling").Line()
		emitToolCatalogTypes(stmt)
		emitToolCallResultCollector(stmt)
		emitDecodeMCPPayloadStrict(stmt)
		emitDecodeMCPPayloadFields(stmt)
		emitValidateMCPPayloadRequired(stmt)
		emitValidateMCPPayloadEnum(stmt)
		emitToolSearchPayloadTypes(stmt)
		emitGeneratedToolCatalog(stmt, data)
		emitToolSearchHelpers(stmt, data)
		emitToolStreamBridges(stmt, data)
		for _, tool := range data.Tools {
			if !tool.HasPayload {
				continue
			}
			emitToolInputRecovery(stmt, tool)
		}
		emitToolsCall(stmt)
		emitToolsCallHandler(stmt, data)
	})
}

func emitToolCatalogTypes(stmt *jen.Statement) {
	stmt.Type().Id("Icon").Struct(
		jen.Id("Src").String().Tag(map[string]string{"json": "src"}),
		jen.Id("MimeType").Op("*").String().Tag(map[string]string{"json": "mimeType,omitempty"}),
		jen.Id("Sizes").Index().String().Tag(map[string]string{"json": "sizes,omitempty"}),
		jen.Id("Theme").Op("*").String().Tag(map[string]string{"json": "theme,omitempty"}),
	)
	stmt.Line()
	stmt.Type().Id("ToolInfo").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Title").Op("*").String().Tag(map[string]string{"json": "title,omitempty"}),
		jen.Id("Description").Op("*").String().Tag(map[string]string{"json": "description,omitempty"}),
		jen.Id("InputSchema").Any().Tag(map[string]string{"json": "inputSchema,omitempty"}),
		jen.Id("OutputSchema").Any().Tag(map[string]string{"json": "outputSchema,omitempty"}),
		jen.Id("Annotations").Any().Tag(map[string]string{"json": "annotations,omitempty"}),
		jen.Id("Meta").Any().Tag(map[string]string{"json": "_meta,omitempty"}),
		jen.Id("Icons").Index().Op("*").Id("Icon").Tag(map[string]string{"json": "icons,omitempty"}),
		jen.Id("LocalTags").Index().String().Tag(map[string]string{"json": "-"}),
		jen.Id("LocalMeta").Map(jen.String()).Index().String().Tag(map[string]string{"json": "-"}),
	)
	stmt.Line()
}

func emitToolCallResultCollector(stmt *jen.Statement) {
	stmt.Type().Id("toolCallResultCollector").Struct(
		jen.Id("adapter").Op("*").Id("MCPAdapter"),
		jen.Id("parts").Index().Op("*").Id("ToolsCallResult"),
		jen.Id("final").Op("*").Id("ToolsCallResult"),
		jen.Id("streamErr").Error(),
	)
	stmt.Line()
	stmt.Func().Id("newToolCallResultCollector").Params(jen.Id("adapter").Op("*").Id("MCPAdapter")).Op("*").Id("toolCallResultCollector").Block(
		jen.Return(jen.Op("&").Id("toolCallResultCollector").Values(jen.Dict{jen.Id("adapter"): jen.Id("adapter")})),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("toolCallResultCollector")).Id("Send").
		Params(jen.Id("_").Qual("context", "Context"), jen.Id("result").Op("*").Id("ToolsCallResult")).Error().
		Block(
			jen.Id("c").Dot("parts").Op("=").Append(jen.Id("c").Dot("parts"), jen.Id("result")),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("toolCallResultCollector")).Id("SendAndClose").
		Params(jen.Id("_").Qual("context", "Context"), jen.Id("result").Op("*").Id("ToolsCallResult")).Error().
		Block(
			jen.Id("c").Dot("final").Op("=").Id("result"),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("toolCallResultCollector")).Id("SendError").
		Params(jen.Id("_").Qual("context", "Context"), jen.Id("_").Any(), jen.Id("err").Error()).Error().
		Block(
			jen.Id("c").Dot("streamErr").Op("=").Id("err"),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("toolCallResultCollector")).Id("result").Params().Op("*").Id("ToolsCallResult").Block(
		jen.If(jen.Id("c").Op("==").Nil()).Block(
			jen.Return(jen.Op("&").Id("ToolsCallResult").Values()),
		),
		jen.If(jen.Id("c").Dot("streamErr").Op("!=").Nil()).Block(
			jen.Id("mapped").Op(":=").Id("c").Dot("streamErr"),
			jen.If(jen.Id("c").Dot("adapter").Op("!=").Nil()).Block(
				jen.Id("mapped").Op("=").Id("c").Dot("adapter").Dot("mapError").Call(jen.Id("mapped")),
			),
			jen.Id("isError").Op(":=").True(),
			jen.Return(jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
				jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
					jen.Id("buildContentItem").Call(jen.Id("c").Dot("adapter"), jen.Id("formatToolErrorText").Call(jen.Id("mapped"))),
				),
				jen.Id("IsError"): jen.Op("&").Id("isError"),
			})),
		),
		jen.If(jen.Len(jen.Id("c").Dot("parts")).Op("==").Lit(0)).Block(
			jen.If(jen.Id("c").Dot("final").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("ToolsCallResult").Values()),
			),
			jen.Return(jen.Id("c").Dot("final")),
		),
		jen.Id("merged").Op(":=").Op("&").Id("ToolsCallResult").Values(),
		jen.For(jen.List(jen.Id("_"), jen.Id("part")).Op(":=").Range().Append(jen.Id("c").Dot("parts"), jen.Id("c").Dot("final"))).Block(
			jen.If(jen.Id("part").Op("==").Nil()).Block(jen.Continue()),
			jen.Id("merged").Dot("Content").Op("=").Append(jen.Id("merged").Dot("Content"), jen.Id("part").Dot("Content").Op("...")),
			jen.If(jen.Id("mcpJSONPresent").Call(jen.Id("part").Dot("StructuredContent"))).Block(
				jen.Id("merged").Dot("StructuredContent").Op("=").Id("part").Dot("StructuredContent"),
			),
			jen.If(jen.Id("part").Dot("IsError").Op("!=").Nil()).Block(
				jen.Id("value").Op(":=").Op("*").Id("part").Dot("IsError"),
				jen.Id("merged").Dot("IsError").Op("=").Op("&").Id("value"),
			),
		),
		jen.Return(jen.Id("merged")),
	)
	stmt.Line()
}

// toolRecoveryFuncName returns the generated per-tool recovery function name.
func toolRecoveryFuncName(tool *ToolAdapter) string {
	return codegen.Goify(tool.OriginalMethodName, false) + "InputRecovery"
}

// emitToolInputRecovery emits a per-tool function that turns a decoder/validation
// error into a recovery hint string. The hint references a canonical example
// synthesized from the IR at codegen time; for tools whose payload contains a
// single union envelope, a sibling <tool>ExampleForRaw function reads the
// caller's discriminator and returns the per-tag example when valid, so a
// "valid tag + malformed inner branch" caller does not see their tag rewritten
// by the hint.
func emitToolInputRecovery(stmt *jen.Statement, tool *ToolAdapter) {
	canonical := tool.CanonicalExampleJSON
	if canonical == "" {
		canonical = "{}"
	}
	var dynamicEnv *UnionEnvelopeMeta
	if len(tool.UnionEnvelopes) == 1 && len(tool.UnionEnvelopes[0].TagExamples) > 0 {
		dynamicEnv = &tool.UnionEnvelopes[0]
	}
	if dynamicEnv != nil {
		emitToolExampleSelector(stmt, tool, dynamicEnv, canonical)
	}
	stmt.Func().Id(toolRecoveryFuncName(tool)).
		Params(jen.Id("err").Error(), jen.Id("raw").Id("jsontext").Dot("Value")).
		String().
		BlockFunc(func(g *jen.Group) {
			g.Id("message").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("loom").Dot("ErrorSafeMessage").Call(jen.Id("err")))
			g.If(jen.Id("message").Op("==").Lit("")).Block(
				jen.Id("message").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("err").Dot("Error").Call()),
			)
			if dynamicEnv != nil {
				g.Id("example").Op(":=").Id(toolExampleSelectorName(tool)).Call(jen.Id("raw"))
			} else {
				g.Id("_").Op("=").Id("raw")
				g.Id("example").Op(":=").Lit(canonical)
			}
			for _, env := range tool.UnionEnvelopes {
				if len(env.Tags) == 0 {
					continue
				}
				envSubstr := fmt.Sprintf("invalid value for %q", env.TypeKey)
				hintPrefix := fmt.Sprintf("Field %q must use one of %s for %q. Example: ",
					env.FieldName, FormatTagList(env.Tags), env.TypeKey)
				g.If(jen.Qual("strings", "Contains").Call(jen.Id("message"), jen.Lit(envSubstr))).Block(
					jen.Return(jen.Lit(hintPrefix).Op("+").Id("example")),
				)
			}
			g.If(jen.Id("field").Op(":=").Id("missingFieldFromMessage").Call(jen.Id("message")), jen.Id("field").Op("!=").Lit("")).Block(
				jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("Include required field %q. Example: %s"), jen.Id("field"), jen.Id("example"))),
			)
			g.If(jen.Qual("strings", "Contains").Call(jen.Id("message"), jen.Lit("unexpected end of JSON input")).Op("||").Qual("strings", "Contains").Call(jen.Id("message"), jen.Lit("unexpected EOF"))).Block(
				jen.Return(jen.Lit("Provide complete JSON arguments. Example: ").Op("+").Id("example")),
			)
			g.Return(jen.Lit("Provide valid tool arguments. Example: ").Op("+").Id("example"))
		})
	stmt.Line()
}

func toolExampleSelectorName(tool *ToolAdapter) string {
	return codegen.Goify(tool.OriginalMethodName, false) + "RecoveryExample"
}

// emitToolExampleSelector emits a helper that picks a tag-specific canonical
// example based on the caller's raw input. If the caller's envelope field
// carries a discriminator value that is a member of the declared tag set, the
// matching per-tag example is returned; otherwise the first-tag example is
// returned.
func emitToolExampleSelector(stmt *jen.Statement, tool *ToolAdapter, env *UnionEnvelopeMeta, fallback string) {
	caseStmts := make([]jen.Code, 0, len(env.TagExamples)+1)
	for _, tag := range env.Tags {
		ex, ok := env.TagExamples[tag]
		if !ok {
			continue
		}
		caseStmts = append(caseStmts, jen.Case(jen.Lit(tag)).Block(jen.Return(jen.Lit(ex))))
	}
	stmt.Func().Id(toolExampleSelectorName(tool)).
		Params(jen.Id("raw").Id("jsontext").Dot("Value")).
		String().
		BlockFunc(func(g *jen.Group) {
			g.Const().Id("fallback").Op("=").Lit(fallback)
			g.Var().Id("top").Map(jen.String()).Id("jsontext").Dot("Value")
			g.If(jen.Id("json").Dot("Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("top")).Op("!=").Nil()).Block(
				jen.Return(jen.Id("fallback")),
			)
			g.List(jen.Id("envRaw"), jen.Id("hasEnv")).Op(":=").Id("top").Index(jen.Lit(env.FieldName))
			g.If(jen.Op("!").Id("hasEnv")).Block(jen.Return(jen.Id("fallback")))
			g.Var().Id("envelope").Map(jen.String()).Id("jsontext").Dot("Value")
			g.If(jen.Id("json").Dot("Unmarshal").Call(jen.Id("envRaw"), jen.Op("&").Id("envelope")).Op("!=").Nil()).Block(
				jen.Return(jen.Id("fallback")),
			)
			g.List(jen.Id("tagRaw"), jen.Id("hasTag")).Op(":=").Id("envelope").Index(jen.Lit(env.TypeKey))
			g.If(jen.Op("!").Id("hasTag")).Block(jen.Return(jen.Id("fallback")))
			g.Var().Id("tag").String()
			g.If(jen.Id("json").Dot("Unmarshal").Call(jen.Id("tagRaw"), jen.Op("&").Id("tag")).Op("!=").Nil()).Block(
				jen.Return(jen.Id("fallback")),
			)
			g.Switch(jen.Id("tag")).Block(caseStmts...)
			g.Return(jen.Id("fallback"))
		})
	stmt.Line()
}

func emitDecodeMCPPayloadStrict(stmt *jen.Statement) {
	stmt.Func().Id("decodeMCPPayloadStrict").
		Params(jen.Id("data").Index().Byte(), jen.Id("payload").Any()).
		Error().
		Block(
			jen.Return(jen.Id("json").Dot("Unmarshal").Call(
				jen.Id("data"),
				jen.Id("payload"),
				jen.Id("json").Dot("RejectUnknownMembers").Call(jen.True()),
			)),
		)
	stmt.Line()
}

func emitDecodeMCPPayloadFields(stmt *jen.Statement) {
	stmt.Func().Id("decodeMCPPayloadFields").
		Params(jen.Id("data").Index().Byte()).
		Params(jen.Map(jen.String()).Id("jsontext").Dot("Value"), jen.Error()).
		Block(
			jen.Var().Id("fields").Map(jen.String()).Id("jsontext").Dot("Value"),
			jen.If(jen.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("fields")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.If(jen.Id("fields").Op("==").Nil()).Block(
				jen.Id("fields").Op("=").Make(jen.Map(jen.String()).Id("jsontext").Dot("Value")),
			),
			jen.Return(jen.Id("fields"), jen.Nil()),
		)
	stmt.Line()
}

func emitValidateMCPPayloadRequired(stmt *jen.Statement) {
	stmt.Func().Id("validateMCPPayloadRequired").
		Params(
			jen.Id("fields").Map(jen.String()).Id("jsontext").Dot("Value"),
			jen.Id("field").String(),
			jen.Id("allowsNull").Bool(),
		).
		Error().
		Block(
			jen.List(jen.Id("raw"), jen.Id("ok")).Op(":=").Id("fields").Index(jen.Id("field")),
			jen.If(jen.Op("!").Id("ok")).Block(
				jen.Return(requiredFieldErrorExpr(jen.Id("field"))),
			),
			jen.If(
				jen.Op("!").Id("allowsNull").Op("&&").
					Qual("bytes", "Equal").Call(jen.Qual("bytes", "TrimSpace").Call(jen.Id("raw")), jen.Index().Byte().Call(jen.Lit("null"))),
			).Block(
				jen.Return(requiredFieldErrorExpr(jen.Id("field"))),
			),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
}

// requiredFieldErrorExpr emits the error returned when a required payload
// field is missing. The error carries Code + SafeMessage only; the per-tool
// recovery function attaches the DSL-derived retry hint (including a canonical
// example), so omitting RetryHint here is intentional — it would otherwise
// pre-empt the richer per-tool hint inside toolCallError.
func requiredFieldErrorExpr(field jen.Code) jen.Code {
	return jen.Id("loom").Dot("WithErrorRemedy").Call(
		jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing required field: %s"), field),
		jen.Op("&").Id("loom").Dot("ErrorRemedy").Values(jen.Dict{
			jen.Id("Code"):        jen.Lit("invalid_params"),
			jen.Id("SafeMessage"): jen.Qual("fmt", "Sprintf").Call(jen.Lit("Missing required field: %s"), field),
		}),
	)
}

func emitValidateMCPPayloadEnum(stmt *jen.Statement) {
	stmt.Func().Id("validateMCPPayloadEnum").
		Params(
			jen.Id("fields").Map(jen.String()).Id("jsontext").Dot("Value"),
			jen.Id("field").String(),
			jen.Id("optional").Bool(),
			jen.Id("allowed").Op("...").String(),
		).
		Error().
		Block(
			jen.List(jen.Id("raw"), jen.Id("ok")).Op(":=").Id("fields").Index(jen.Id("field")),
			jen.If(jen.Op("!").Id("ok")).Block(
				jen.Return(jen.Nil()),
			),
			jen.Id("trimmed").Op(":=").Qual("bytes", "TrimSpace").Call(jen.Id("raw")),
			jen.If(jen.Id("optional").Op("&&").Qual("bytes", "Equal").Call(jen.Id("trimmed"), jen.Index().Byte().Call(jen.Lit("null")))).Block(
				jen.Return(jen.Nil()),
			),
			jen.Var().Id("value").Any(),
			jen.If(jen.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("value")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			),
			jen.Id("actual").Op(":=").Qual("fmt", "Sprint").Call(jen.Id("value")),
			jen.For(jen.List(jen.Id("_"), jen.Id("candidate")).Op(":=").Range().Id("allowed")).Block(
				jen.If(jen.Id("actual").Op("==").Id("candidate")).Block(
					jen.Return(jen.Nil()),
				),
			),
			jen.Return(
				jen.Id("loom").Dot("WithErrorRemedy").Call(
					jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Invalid value for %s"), jen.Id("field")),
					jen.Op("&").Id("loom").Dot("ErrorRemedy").Values(jen.Dict{
						jen.Id("Code"):        jen.Lit("invalid_params"),
						jen.Id("SafeMessage"): jen.Qual("fmt", "Sprintf").Call(jen.Lit("Invalid value for %s"), jen.Id("field")),
					}),
				),
			),
		)
	stmt.Line()
}

func emitToolSearchPayloadTypes(stmt *jen.Statement) {
	stmt.Type().Id("toolSearchPayload").Struct(
		jen.Id("Query").String().Tag(map[string]string{"json": "query,omitempty"}),
		jen.Id("Pattern").String().Tag(map[string]string{"json": "pattern"}),
		jen.Id("MaxResults").Op("*").Int().Tag(map[string]string{"json": "max_results,omitempty"}),
		jen.Id("IncludeSchemas").Bool().Tag(map[string]string{"json": "include_schemas,omitempty"}),
		jen.Id("Category").String().Tag(map[string]string{"json": "category,omitempty"}),
		jen.Id("Tags").Index().String().Tag(map[string]string{"json": "tags,omitempty"}),
	)
	stmt.Line()

	stmt.Type().Id("toolCallProxyPayload").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Arguments").Id("jsontext").Dot("Value").Tag(map[string]string{"json": "arguments,omitempty"}),
	)
	stmt.Line()

	stmt.Type().Id("toolSearchResult").Struct(
		jen.Id("Tools").Index().Id("toolSearchDescriptor").Tag(map[string]string{"json": "tools"}),
		jen.Id("TotalMatches").Int().Tag(map[string]string{"json": "total_matches"}),
		jen.Id("Truncated").Bool().Tag(map[string]string{"json": "truncated"}),
		jen.Id("Query").String().Tag(map[string]string{"json": "query,omitempty"}),
		jen.Id("Pattern").String().Tag(map[string]string{"json": "pattern,omitempty"}),
	)
	stmt.Line()

	stmt.Type().Id("toolSearchDescriptor").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Title").String().Tag(map[string]string{"json": "title,omitempty"}),
		jen.Id("Description").String().Tag(map[string]string{"json": "description,omitempty"}),
		jen.Id("InputSchema").Id("jsontext").Dot("Value").Tag(map[string]string{"json": "inputSchema,omitempty"}),
		jen.Id("OutputSchema").Id("jsontext").Dot("Value").Tag(map[string]string{"json": "outputSchema,omitempty"}),
		jen.Id("Annotations").Id("jsontext").Dot("Value").Tag(map[string]string{"json": "annotations,omitempty"}),
		jen.Id("Meta").Id("jsontext").Dot("Value").Tag(map[string]string{"json": "_meta,omitempty"}),
		jen.Id("Icons").Index().Op("*").Id("Icon").Tag(map[string]string{"json": "icons,omitempty"}),
		jen.Id("Category").String().Tag(map[string]string{"json": "category,omitempty"}),
		jen.Id("Tags").Index().String().Tag(map[string]string{"json": "tags,omitempty"}),
		jen.Id("Keywords").Index().String().Tag(map[string]string{"json": "keywords,omitempty"}),
		jen.Id("WhyMatched").Index().String().Tag(map[string]string{"json": "why_matched,omitempty"}),
		jen.Id("CallToolName").String().Tag(map[string]string{"json": "call_tool_name,omitempty"}),
		jen.Id("CallToolArguments").Id("jsontext").Dot("Value").Tag(map[string]string{"json": "call_tool_arguments,omitempty"}),
		jen.Id("CallToolJSON").String().Tag(map[string]string{"json": "call_tool_json,omitempty"}),
	)
	stmt.Line()

	stmt.Type().Id("toolSearchCandidate").Struct(
		jen.Id("tool").Op("*").Id("ToolInfo"),
		jen.Id("score").Int(),
		jen.Id("order").Int(),
	)
	stmt.Line()

	stmt.Type().Id("toolSearchSettings").Struct(
		jen.Id("maxResults").Int(),
		jen.Id("minScore").Int(),
		jen.Id("exactMatchMode").String(),
		jen.Id("fuzzyNameMatching").Bool(),
		jen.Id("broadFallback").Bool(),
		jen.Id("nameWeight").Int(),
		jen.Id("titleWeight").Int(),
		jen.Id("metadataWeight").Int(),
		jen.Id("descriptionWeight").Int(),
		jen.Id("parameterWeight").Int(),
		jen.Id("fuzzyNameWeight").Int(),
	)
	stmt.Line()
}

func toolInfoValue(tool *ToolAdapter) jen.Code {
	dict := jen.Dict{
		jen.Id("Name"):        jen.Lit(tool.Name),
		jen.Id("Title"):       jen.Id("stringPtr").Call(jen.Lit(tool.Title)),
		jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit(tool.Description)),
	}
	switch {
	case tool.Projected != nil:
		dict[jen.Id("InputSchema")] = jen.Id("jsontext").Dot("Value").Call(
			jen.Id(tool.Projected.SpecsPackageName).Dot(tool.Projected.SpecName).Dot("Payload").Dot("Schema"),
		)
	case tool.InputSchema != "":
		dict[jen.Id("InputSchema")] = jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.InputSchema)))
	default:
		dict[jen.Id("InputSchema")] = jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(`{"type":"object","properties":{},"additionalProperties":false}`)))
	}
	switch {
	case tool.Projected != nil && tool.Projected.HasResult:
		dict[jen.Id("OutputSchema")] = jen.Id("jsontext").Dot("Value").Call(
			jen.Id(tool.Projected.SpecsPackageName).Dot(tool.Projected.SpecName).Dot("Result").Dot("Schema"),
		)
	case tool.OutputSchema != "":
		dict[jen.Id("OutputSchema")] = jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.OutputSchema)))
	}
	if tool.AnnotationsJSON != "" {
		dict[jen.Id("Annotations")] = jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.AnnotationsJSON)))
	}
	if tool.MetaJSON != "" {
		dict[jen.Id("Meta")] = jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.MetaJSON)))
	}
	if icons := iconSliceValue(tool.Icons); icons != nil {
		dict[jen.Id("Icons")] = icons
	}
	if tool.Projected != nil {
		dict[jen.Id("LocalTags")] = jen.Id(tool.Projected.SpecsPackageName).Dot(tool.Projected.SpecName).Dot("Tags")
		dict[jen.Id("LocalMeta")] = jen.Id(tool.Projected.SpecsPackageName).Dot(tool.Projected.SpecName).Dot("Meta")
	}
	return jen.Op("&").Id("ToolInfo").Values(dict)
}

func emitGeneratedToolCatalog(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("generatedToolCatalog").Params().Index().Op("*").Id("ToolInfo").
		Block(
			jen.Return(jen.Index().Op("*").Id("ToolInfo").ValuesFunc(func(vals *jen.Group) {
				for _, tool := range data.Tools {
					vals.Add(toolInfoValue(tool))
				}
			})),
		)
	stmt.Line()
}

func emitToolSearchHelpers(stmt *jen.Statement, data *AdapterData) {
	emitToolSearchEnabled(stmt)
	emitToolSearchNames(stmt)
	emitGeneratedToolNameHelpers(stmt, data)
	emitValidateToolSearchOptions(stmt, data)
	emitToolSearchSettings(stmt, data)
	emitVisibleToolCatalog(stmt)
	emitToolSearchSyntheticTools(stmt)
	emitToolSearchToolInfo(stmt)
	emitToolCallProxyToolInfo(stmt)
	emitToolSearchHaystack(stmt)
	emitToolSearchDescriptorHelpers(stmt)
	emitHandleSearchTools(stmt)
	emitHandleCallToolProxy(stmt)
}

func emitToolSearchEnabled(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("toolSearchEnabled").Params().Bool().
		Block(
			jen.Return(jen.Id("a").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Dot("ToolSearch").Op("!=").Nil()),
		)
	stmt.Line()
}

func emitToolSearchNames(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("toolSearchNames").Params().Params(jen.String(), jen.String()).
		Block(
			jen.Id("searchName").Op(":=").Lit("search_tools"),
			jen.Id("callName").Op(":=").Lit("call_tool"),
			jen.If(jen.Id("a").Dot("toolSearchEnabled").Call()).Block(
				jen.If(jen.Id("a").Dot("opts").Dot("ToolSearch").Dot("SearchToolName").Op("!=").Lit("")).Block(
					jen.Id("searchName").Op("=").Id("a").Dot("opts").Dot("ToolSearch").Dot("SearchToolName"),
				),
				jen.If(jen.Id("a").Dot("opts").Dot("ToolSearch").Dot("CallToolName").Op("!=").Lit("")).Block(
					jen.Id("callName").Op("=").Id("a").Dot("opts").Dot("ToolSearch").Dot("CallToolName"),
				),
			),
			jen.Return(jen.Id("searchName"), jen.Id("callName")),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("isToolSearchName").Params(jen.Id("name").String()).Bool().
		Block(
			jen.Id("searchName").Op(",").Id("_").Op(":=").Id("a").Dot("toolSearchNames").Call(),
			jen.Return(jen.Id("a").Dot("toolSearchEnabled").Call().Op("&&").Id("name").Op("==").Id("searchName")),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("isToolCallProxyName").Params(jen.Id("name").String()).Bool().
		Block(
			jen.Id("_").Op(",").Id("callName").Op(":=").Id("a").Dot("toolSearchNames").Call(),
			jen.Return(jen.Id("a").Dot("toolSearchEnabled").Call().Op("&&").Id("name").Op("==").Id("callName")),
		)
	stmt.Line()
}

func emitGeneratedToolNameHelpers(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("isGeneratedToolName").Params(jen.Id("name").String()).Bool().
		Block(
			jen.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, tool := range data.Tools {
					sw.Case(jen.Lit(tool.Name)).Block(jen.Return(jen.True()))
				}
				sw.Default().Block(jen.Return(jen.False()))
			}),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("isAlwaysVisibleToolName").Params(jen.Id("name").String()).Bool().
		Block(
			jen.If(jen.Op("!").Id("a").Dot("toolSearchEnabled").Call()).Block(
				jen.Return(jen.True()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("visible")).Op(":=").Range().Id("a").Dot("opts").Dot("ToolSearch").Dot("AlwaysVisible")).Block(
				jen.If(jen.Id("visible").Op("==").Id("name")).Block(
					jen.Return(jen.True()),
				),
			),
			jen.Return(jen.False()),
		)
	stmt.Line()
}

func emitValidateToolSearchOptions(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("validateToolSearchOptions").Params(jen.Id("opts").Op("*").Id("MCPAdapterOptions")).Block(
		jen.If(jen.Id("opts").Op("==").Nil().Op("||").Id("opts").Dot("ToolSearch").Op("==").Nil()).Block(
			jen.Return(),
		),
		jen.Id("searchName").Op(":=").Lit("search_tools"),
		jen.Id("callName").Op(":=").Lit("call_tool"),
		jen.If(jen.Id("opts").Dot("ToolSearch").Dot("SearchToolName").Op("!=").Lit("")).Block(
			jen.Id("searchName").Op("=").Id("opts").Dot("ToolSearch").Dot("SearchToolName"),
		),
		jen.If(jen.Id("opts").Dot("ToolSearch").Dot("CallToolName").Op("!=").Lit("")).Block(
			jen.Id("callName").Op("=").Id("opts").Dot("ToolSearch").Dot("CallToolName"),
		),
		jen.If(jen.Id("searchName").Op("==").Id("callName")).Block(
			jen.Panic(jen.Qual("fmt", "Sprintf").Call(jen.Lit("MCP ToolSearch synthetic tool names must be distinct: %q"), jen.Id("searchName"))),
		),
		jen.If(jen.Id("opts").Dot("ToolSearch").Dot("MaxResults").Op("<").Lit(0)).Block(
			jen.Panic(jen.Lit("MCP ToolSearch MaxResults must be non-negative")),
		),
		jen.If(jen.Id("opts").Dot("ToolSearch").Dot("MinScore").Op("<").Lit(0)).Block(
			jen.Panic(jen.Lit("MCP ToolSearch MinScore must be non-negative")),
		),
		jen.Switch(jen.Id("opts").Dot("ToolSearch").Dot("ExactMatchMode")).Block(
			jen.Case(jen.Lit(""), jen.Lit("narrow"), jen.Lit("boost"), jen.Lit("off")).Block(),
			jen.Default().Block(jen.Panic(jen.Qual("fmt", "Sprintf").Call(jen.Lit("MCP ToolSearch ExactMatchMode must be narrow, boost, or off: %q"), jen.Id("opts").Dot("ToolSearch").Dot("ExactMatchMode")))),
		),
		jen.If(jen.Id("opts").Dot("ToolSearch").Dot("Weights").Op("!=").Nil()).Block(
			jen.Id("weights").Op(":=").Id("opts").Dot("ToolSearch").Dot("Weights"),
			jen.If(jen.Id("weights").Dot("Name").Op("<").Lit(0).Op("||").Id("weights").Dot("Title").Op("<").Lit(0).Op("||").Id("weights").Dot("Metadata").Op("<").Lit(0).Op("||").Id("weights").Dot("Description").Op("<").Lit(0).Op("||").Id("weights").Dot("Parameters").Op("<").Lit(0).Op("||").Id("weights").Dot("FuzzyName").Op("<").Lit(0)).Block(
				jen.Panic(jen.Lit("MCP ToolSearch weights must be non-negative")),
			),
		),
		jen.If(jen.Id("isGeneratedToolName").Call(jen.Id("searchName"))).Block(
			jen.Panic(jen.Qual("fmt", "Sprintf").Call(jen.Lit("MCP ToolSearch search tool name %q collides with a generated tool"), jen.Id("searchName"))),
		),
		jen.If(jen.Id("isGeneratedToolName").Call(jen.Id("callName"))).Block(
			jen.Panic(jen.Qual("fmt", "Sprintf").Call(jen.Lit("MCP ToolSearch call tool name %q collides with a generated tool"), jen.Id("callName"))),
		),
		jen.For(jen.List(jen.Id("_"), jen.Id("name")).Op(":=").Range().Id("opts").Dot("ToolSearch").Dot("AlwaysVisible")).Block(
			jen.If(jen.Id("name").Op("==").Lit("")).Block(jen.Continue()),
			jen.If(jen.Op("!").Id("isGeneratedToolName").Call(jen.Id("name"))).Block(
				jen.Panic(jen.Qual("fmt", "Sprintf").Call(jen.Lit("MCP ToolSearch AlwaysVisible tool %q is not a generated tool"), jen.Id("name"))),
			),
			jen.If(jen.Id("name").Op("==").Id("searchName").Op("||").Id("name").Op("==").Id("callName")).Block(
				jen.Panic(jen.Qual("fmt", "Sprintf").Call(jen.Lit("MCP ToolSearch AlwaysVisible tool %q collides with a synthetic tool"), jen.Id("name"))),
			),
		),
	)
	stmt.Line()

	_ = data
}

func emitToolSearchSettings(stmt *jen.Statement, data *AdapterData) {
	settings := data.ToolSearch
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("toolSearchSettings").Params().Id("toolSearchSettings").
		Block(
			jen.Id("settings").Op(":=").Id("toolSearchSettings").Values(jen.Dict{
				jen.Id("maxResults"):        jen.Lit(settings.DefaultMaxResults),
				jen.Id("minScore"):          jen.Lit(settings.MinScore),
				jen.Id("exactMatchMode"):    jen.Lit(settings.ExactMatchMode),
				jen.Id("fuzzyNameMatching"): jen.Lit(settings.FuzzyNameMatching),
				jen.Id("broadFallback"):     jen.Lit(settings.BroadFallback),
				jen.Id("nameWeight"):        jen.Lit(settings.NameWeight),
				jen.Id("titleWeight"):       jen.Lit(settings.TitleWeight),
				jen.Id("metadataWeight"):    jen.Lit(settings.MetadataWeight),
				jen.Id("descriptionWeight"): jen.Lit(settings.DescriptionWeight),
				jen.Id("parameterWeight"):   jen.Lit(settings.ParameterWeight),
				jen.Id("fuzzyNameWeight"):   jen.Lit(settings.FuzzyNameWeight),
			}),
			jen.If(jen.Id("a").Dot("toolSearchEnabled").Call()).Block(
				jen.Id("opts").Op(":=").Id("a").Dot("opts").Dot("ToolSearch"),
				jen.If(jen.Id("opts").Dot("MaxResults").Op(">").Lit(0)).Block(jen.Id("settings").Dot("maxResults").Op("=").Id("opts").Dot("MaxResults")),
				jen.If(jen.Id("opts").Dot("MinScore").Op(">").Lit(0)).Block(jen.Id("settings").Dot("minScore").Op("=").Id("opts").Dot("MinScore")),
				jen.If(jen.Id("opts").Dot("ExactMatchMode").Op("!=").Lit("")).Block(jen.Id("settings").Dot("exactMatchMode").Op("=").Id("opts").Dot("ExactMatchMode")),
				jen.If(jen.Id("opts").Dot("FuzzyNameMatching").Op("!=").Nil()).Block(jen.Id("settings").Dot("fuzzyNameMatching").Op("=").Op("*").Id("opts").Dot("FuzzyNameMatching")),
				jen.If(jen.Id("opts").Dot("BroadFallback").Op("!=").Nil()).Block(jen.Id("settings").Dot("broadFallback").Op("=").Op("*").Id("opts").Dot("BroadFallback")),
				jen.If(jen.Id("opts").Dot("Weights").Op("!=").Nil()).Block(
					jen.If(jen.Id("opts").Dot("Weights").Dot("Name").Op(">").Lit(0)).Block(jen.Id("settings").Dot("nameWeight").Op("=").Id("opts").Dot("Weights").Dot("Name")),
					jen.If(jen.Id("opts").Dot("Weights").Dot("Title").Op(">").Lit(0)).Block(jen.Id("settings").Dot("titleWeight").Op("=").Id("opts").Dot("Weights").Dot("Title")),
					jen.If(jen.Id("opts").Dot("Weights").Dot("Metadata").Op(">").Lit(0)).Block(jen.Id("settings").Dot("metadataWeight").Op("=").Id("opts").Dot("Weights").Dot("Metadata")),
					jen.If(jen.Id("opts").Dot("Weights").Dot("Description").Op(">").Lit(0)).Block(jen.Id("settings").Dot("descriptionWeight").Op("=").Id("opts").Dot("Weights").Dot("Description")),
					jen.If(jen.Id("opts").Dot("Weights").Dot("Parameters").Op(">").Lit(0)).Block(jen.Id("settings").Dot("parameterWeight").Op("=").Id("opts").Dot("Weights").Dot("Parameters")),
					jen.If(jen.Id("opts").Dot("Weights").Dot("FuzzyName").Op(">").Lit(0)).Block(jen.Id("settings").Dot("fuzzyNameWeight").Op("=").Id("opts").Dot("Weights").Dot("FuzzyName")),
				),
			),
			jen.Return(jen.Id("settings")),
		)
	stmt.Line()
}

func emitVisibleToolCatalog(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("visibleToolCatalog").Params(jen.Id("tools").Index().Op("*").Id("ToolInfo")).Index().Op("*").Id("ToolInfo").
		Block(
			jen.If(jen.Op("!").Id("a").Dot("toolSearchEnabled").Call()).Block(
				jen.Return(jen.Id("tools")),
			),
			jen.Id("pinned").Op(":=").Make(jen.Map(jen.String()).Struct(), jen.Len(jen.Id("a").Dot("opts").Dot("ToolSearch").Dot("AlwaysVisible"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("name")).Op(":=").Range().Id("a").Dot("opts").Dot("ToolSearch").Dot("AlwaysVisible")).Block(
				jen.If(jen.Id("name").Op("!=").Lit("")).Block(
					jen.Id("pinned").Index(jen.Id("name")).Op("=").Struct().Values(),
				),
			),
			jen.Id("visible").Op(":=").Make(jen.Index().Op("*").Id("ToolInfo"), jen.Lit(0), jen.Len(jen.Id("pinned")).Op("+").Lit(2)),
			jen.For(jen.List(jen.Id("_"), jen.Id("tool")).Op(":=").Range().Id("tools")).Block(
				jen.If(jen.Id("tool").Op("==").Nil()).Block(jen.Continue()),
				jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("pinned").Index(jen.Id("tool").Dot("Name")), jen.Id("ok")).Block(
					jen.Id("visible").Op("=").Append(jen.Id("visible"), jen.Id("tool")),
				),
			),
			jen.Return(jen.Id("visible")),
		)
	stmt.Line()
}

func emitToolSearchSyntheticTools(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("toolSearchSyntheticTools").Params().Index().Op("*").Id("ToolInfo").
		Block(
			jen.Id("searchName").Op(",").Id("callName").Op(":=").Id("a").Dot("toolSearchNames").Call(),
			jen.Return(jen.Index().Op("*").Id("ToolInfo").Values(
				jen.Id("toolSearchToolInfo").Call(jen.Id("searchName")),
				jen.Id("toolCallProxyToolInfo").Call(jen.Id("callName")),
			)),
		)
	stmt.Line()
}

func emitToolSearchToolInfo(stmt *jen.Statement) {
	stmt.Func().Id("toolSearchToolInfo").Params(jen.Id("name").String()).Op("*").Id("ToolInfo").
		Block(
			jen.Return(jen.Op("&").Id("ToolInfo").Values(jen.Dict{
				jen.Id("Name"):         jen.Id("name"),
				jen.Id("Title"):        jen.Id("stringPtr").Call(jen.Lit("Search Tools")),
				jen.Id("Description"):  jen.Id("stringPtr").Call(jen.Lit("Search available tools by plain text query or regex pattern and return matching tool definitions.")),
				jen.Id("InputSchema"):  jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(`{"type":"object","properties":{"query":{"type":"string","description":"Plain text query matched against tool names, titles, descriptions, metadata, and schemas"},"pattern":{"type":"string","description":"Case-insensitive regex pattern matched against tool names, titles, descriptions, metadata, and schemas"},"max_results":{"type":"integer","description":"Maximum number of tools to return for this search"},"include_schemas":{"type":"boolean","description":"Include input and output schemas in returned descriptors"},"category":{"type":"string","description":"Discovery category filter"},"tags":{"type":"array","items":{"type":"string"},"description":"Discovery tag filters"}},"additionalProperties":false}`))),
				jen.Id("OutputSchema"): jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(`{"type":"object","required":["tools","total_matches","truncated"],"properties":{"tools":{"type":"array","items":{"type":"object","required":["name","description"],"properties":{"name":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},"inputSchema":{"type":"object"},"outputSchema":{"type":"object"},"annotations":{"type":"object"},"_meta":{"type":"object"},"icons":{"type":"array","items":{"type":"object"}},"category":{"type":"string"},"tags":{"type":"array","items":{"type":"string"}},"keywords":{"type":"array","items":{"type":"string"}},"why_matched":{"type":"array","items":{"type":"string"}},"call_tool_name":{"type":"string"},"call_tool_arguments":{"type":"object"},"call_tool_json":{"type":"string"}}}},"total_matches":{"type":"integer"},"truncated":{"type":"boolean"},"query":{"type":"string"},"pattern":{"type":"string"}},"additionalProperties":false}`))),
			})),
		)
	stmt.Line()
}

func emitToolCallProxyToolInfo(stmt *jen.Statement) {
	stmt.Func().Id("toolCallProxyToolInfo").Params(jen.Id("name").String()).Op("*").Id("ToolInfo").
		Block(
			jen.Return(jen.Op("&").Id("ToolInfo").Values(jen.Dict{
				jen.Id("Name"):        jen.Id("name"),
				jen.Id("Title"):       jen.Id("stringPtr").Call(jen.Lit("Call Tool")),
				jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit("Call a discovered tool by exact name. Always provide both top-level fields: name and arguments. Use arguments: {} when the discovered tool takes no arguments. Do not use args.")),
				jen.Id("InputSchema"): jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(`{"type":"object","required":["name","arguments"],"properties":{"name":{"type":"string","description":"Exact discovered tool name. Required. Copy this from search_tools results."},"arguments":{"type":"object","description":"Arguments object for the discovered tool. Required. Use {} when the discovered tool takes no arguments. Do not use args."}},"additionalProperties":false}`))),
			})),
		)
	stmt.Line()
}

func emitToolSearchHaystack(stmt *jen.Statement) {
	stmt.Func().Id("toolSearchHaystack").Params(jen.Id("tool").Op("*").Id("ToolInfo")).String().
		Block(
			jen.If(jen.Id("tool").Op("==").Nil()).Block(jen.Return(jen.Lit(""))),
			jen.Id("parts").Op(":=").Index().String().Values(jen.Id("tool").Dot("Name")),
			jen.If(jen.Id("tool").Dot("Description").Op("!=").Nil()).Block(
				jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.Op("*").Id("tool").Dot("Description")),
			),
			jen.If(jen.Id("tool").Dot("Title").Op("!=").Nil()).Block(
				jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.Op("*").Id("tool").Dot("Title")),
			),
			jen.Switch(jen.Id("schema").Op(":=").Id("tool").Dot("InputSchema").Assert(jen.Type())).Block(
				jen.Case(jen.Id("jsontext").Dot("Value")).Block(
					jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.String().Call(jen.Id("schema"))),
				),
				jen.Case(jen.Index().Byte()).Block(
					jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.String().Call(jen.Id("schema"))),
				),
				jen.Default().Block(
					jen.If(jen.Id("schema").Op("!=").Nil()).Block(
						jen.If(jen.List(jen.Id("raw"), jen.Id("err")).Op(":=").Id("json").Dot("Marshal").Call(jen.Id("schema")), jen.Id("err").Op("==").Nil()).Block(
							jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.String().Call(jen.Id("raw"))),
						),
					),
				),
			),
			jen.Switch(jen.Id("schema").Op(":=").Id("tool").Dot("OutputSchema").Assert(jen.Type())).Block(
				jen.Case(jen.Id("jsontext").Dot("Value")).Block(
					jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.String().Call(jen.Id("schema"))),
				),
				jen.Case(jen.Index().Byte()).Block(
					jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.String().Call(jen.Id("schema"))),
				),
			),
			jen.Switch(jen.Id("meta").Op(":=").Id("tool").Dot("Meta").Assert(jen.Type())).Block(
				jen.Case(jen.Id("jsontext").Dot("Value")).Block(
					jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.String().Call(jen.Id("meta"))),
				),
				jen.Case(jen.Index().Byte()).Block(
					jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.String().Call(jen.Id("meta"))),
				),
			),
			jen.Return(jen.Qual("strings", "Join").Call(jen.Id("parts"), jen.Lit(" "))),
		)
	stmt.Line()
}

func emitToolSearchDescriptorHelpers(stmt *jen.Statement) {
	stmt.Func().Id("toolRawJSON").Params(jen.Id("value").Any()).Id("jsontext").Dot("Value").
		Block(
			jen.Switch(jen.Id("v").Op(":=").Id("value").Assert(jen.Type())).Block(
				jen.Case(jen.Id("jsontext").Dot("Value")).Block(jen.Return(jen.Id("v"))),
				jen.Case(jen.Index().Byte()).Block(jen.Return(jen.Id("jsontext").Dot("Value").Call(jen.Id("v")))),
				jen.Case(jen.String()).Block(jen.Return(jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Id("v"))))),
				jen.Default().Block(
					jen.If(jen.Id("v").Op("==").Nil()).Block(jen.Return(jen.Nil())),
					jen.List(jen.Id("raw"), jen.Id("err")).Op(":=").Id("json").Dot("Marshal").Call(jen.Id("v")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil())),
					jen.Return(jen.Id("jsontext").Dot("Value").Call(jen.Id("raw"))),
				),
			),
		)
	stmt.Line()

	stmt.Func().Id("toolDiscoveryMetadata").Params(jen.Id("tool").Op("*").Id("ToolInfo")).Params(jen.String(), jen.Index().String(), jen.Index().String()).
		Block(
			jen.If(jen.Id("tool").Op("==").Nil()).Block(jen.Return(jen.Lit(""), jen.Nil(), jen.Nil())),
			jen.Id("raw").Op(":=").Id("toolRawJSON").Call(jen.Id("tool").Dot("Meta")),
			jen.If(jen.Len(jen.Id("raw")).Op("==").Lit(0)).Block(jen.Return(jen.Lit(""), jen.Nil(), jen.Nil())),
			jen.Var().Id("meta").Map(jen.String()).Struct(
				jen.Id("Category").String().Tag(map[string]string{"json": "category"}),
				jen.Id("Tags").Index().String().Tag(map[string]string{"json": "tags"}),
				jen.Id("Keywords").Index().String().Tag(map[string]string{"json": "keywords"}),
			),
			jen.If(jen.Id("json").Dot("Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("meta")).Op("!=").Nil()).Block(
				jen.Return(jen.Lit(""), jen.Nil(), jen.Nil()),
			),
			jen.Id("discovery").Op(":=").Id("meta").Index(jen.Lit("com.github.caliluke.loom-mcp/discovery")),
			jen.Return(jen.Id("discovery").Dot("Category"), jen.Id("discovery").Dot("Tags"), jen.Id("discovery").Dot("Keywords")),
		)
	stmt.Line()

	stmt.Func().Id("toolDiscoveryCallTemplateArguments").Params(jen.Id("tool").Op("*").Id("ToolInfo")).Map(jen.String()).Any().
		Block(
			jen.If(jen.Id("tool").Op("==").Nil()).Block(jen.Return(jen.Nil())),
			jen.Id("raw").Op(":=").Id("toolRawJSON").Call(jen.Id("tool").Dot("Meta")),
			jen.If(jen.Len(jen.Id("raw")).Op("==").Lit(0)).Block(jen.Return(jen.Nil())),
			jen.List(jen.Id("meta"), jen.Id("err")).Op(":=").Id("sdkbridge").Dot("DecodeMeta").Call(jen.Id("raw")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil())),
			jen.List(jen.Id("discovery"), jen.Id("ok")).Op(":=").Id("meta").Index(jen.Lit("com.github.caliluke.loom-mcp/discovery")).Assert(jen.Map(jen.String()).Any()),
			jen.If(jen.Op("!").Id("ok")).Block(jen.Return(jen.Nil())),
			jen.List(jen.Id("arguments"), jen.Id("ok")).Op(":=").Id("discovery").Index(jen.Lit("call_template_arguments")).Assert(jen.Map(jen.String()).Any()),
			jen.If(jen.Op("!").Id("ok").Op("||").Len(jen.Id("arguments")).Op("==").Lit(0)).Block(jen.Return(jen.Nil())),
			jen.Return(jen.Id("arguments")),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchDescriptorFor").Params(jen.Id("tool").Op("*").Id("ToolInfo"), jen.Id("includeSchemas").Bool(), jen.Id("callName").String(), jen.Id("query").String(), jen.Id("score").Int(), jen.Id("settings").Id("toolSearchSettings")).Id("toolSearchDescriptor").
		Block(
			jen.Id("category").Op(",").Id("tags").Op(",").Id("keywords").Op(":=").Id("toolDiscoveryMetadata").Call(jen.Id("tool")),
			jen.Id("callArguments").Op(":=").Id("toolCallArgumentsExample").Call(jen.Id("tool")),
			jen.List(jen.Id("callArgumentsJSON"), jen.Id("_")).Op(":=").Id("marshalToolSearchJSON").Call(jen.Id("callArguments")),
			jen.Id("descriptor").Op(":=").Id("toolSearchDescriptor").Values(jen.Dict{
				jen.Id("Name"):              jen.Id("tool").Dot("Name"),
				jen.Id("Category"):          jen.Id("category"),
				jen.Id("Tags"):              jen.Id("tags"),
				jen.Id("Keywords"):          jen.Id("keywords"),
				jen.Id("WhyMatched"):        jen.Id("toolSearchWhyMatched").Call(jen.Id("tool"), jen.Id("query"), jen.Id("score"), jen.Id("settings")),
				jen.Id("CallToolName"):      jen.Id("callName"),
				jen.Id("CallToolArguments"): jen.Id("jsontext").Dot("Value").Call(jen.Id("callArgumentsJSON")),
				jen.Id("CallToolJSON"):      jen.String().Call(jen.Id("callArgumentsJSON")),
			}),
			jen.If(jen.Id("tool").Dot("Title").Op("!=").Nil()).Block(
				jen.Id("descriptor").Dot("Title").Op("=").Op("*").Id("tool").Dot("Title"),
			),
			jen.If(jen.Id("tool").Dot("Description").Op("!=").Nil()).Block(
				jen.Id("descriptor").Dot("Description").Op("=").Op("*").Id("tool").Dot("Description"),
			),
			jen.Id("descriptor").Dot("Annotations").Op("=").Id("toolRawJSON").Call(jen.Id("tool").Dot("Annotations")),
			jen.Id("descriptor").Dot("Meta").Op("=").Id("toolRawJSON").Call(jen.Id("tool").Dot("Meta")),
			jen.Id("descriptor").Dot("Icons").Op("=").Id("tool").Dot("Icons"),
			jen.If(jen.Id("includeSchemas")).Block(
				jen.Id("descriptor").Dot("InputSchema").Op("=").Id("toolRawJSON").Call(jen.Id("tool").Dot("InputSchema")),
				jen.Id("descriptor").Dot("OutputSchema").Op("=").Id("toolRawJSON").Call(jen.Id("tool").Dot("OutputSchema")),
			),
			jen.Return(jen.Id("descriptor")),
		)
	stmt.Line()

	stmt.Func().Id("toolMatchesCategory").Params(jen.Id("tool").Op("*").Id("ToolInfo"), jen.Id("category").String()).Bool().
		Block(
			jen.Id("category").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("category")),
			jen.If(jen.Id("category").Op("==").Lit("")).Block(jen.Return(jen.True())),
			jen.Id("actual").Op(",").Id("_").Op(",").Id("_").Op(":=").Id("toolDiscoveryMetadata").Call(jen.Id("tool")),
			jen.Return(jen.Qual("strings", "EqualFold").Call(jen.Id("actual"), jen.Id("category"))),
		)
	stmt.Line()

	stmt.Func().Id("toolMatchesTags").Params(jen.Id("tool").Op("*").Id("ToolInfo"), jen.Id("required").Index().String()).Bool().
		Block(
			jen.If(jen.Len(jen.Id("required")).Op("==").Lit(0)).Block(jen.Return(jen.True())),
			jen.Id("_").Op(",").Id("tags").Op(",").Id("_").Op(":=").Id("toolDiscoveryMetadata").Call(jen.Id("tool")),
			jen.Id("available").Op(":=").Make(jen.Map(jen.String()).Struct(), jen.Len(jen.Id("tags"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("tag")).Op(":=").Range().Id("tags")).Block(
				jen.Id("available").Index(jen.Qual("strings", "ToLower").Call(jen.Qual("strings", "TrimSpace").Call(jen.Id("tag")))).Op("=").Struct().Values(),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("tag")).Op(":=").Range().Id("required")).Block(
				jen.Id("tag").Op("=").Qual("strings", "ToLower").Call(jen.Qual("strings", "TrimSpace").Call(jen.Id("tag"))),
				jen.If(jen.Id("tag").Op("==").Lit("")).Block(jen.Continue()),
				jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("available").Index(jen.Id("tag")), jen.Op("!").Id("ok")).Block(jen.Return(jen.False())),
			),
			jen.Return(jen.True()),
		)
	stmt.Line()

	stmt.Func().Id("toolInputParameterText").Params(jen.Id("tool").Op("*").Id("ToolInfo")).String().
		Block(
			jen.Id("raw").Op(":=").Id("toolRawJSON").Call(jen.Id("tool").Dot("InputSchema")),
			jen.If(jen.Len(jen.Id("raw")).Op("==").Lit(0)).Block(jen.Return(jen.Lit(""))),
			jen.Var().Id("schema").Struct(jen.Id("Properties").Map(jen.String()).Struct(jen.Id("Description").String().Tag(map[string]string{"json": "description"})).Tag(map[string]string{"json": "properties"})),
			jen.If(jen.Id("json").Dot("Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("schema")).Op("!=").Nil()).Block(jen.Return(jen.Lit(""))),
			jen.Id("parts").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("schema").Dot("Properties")).Op("*").Lit(2)),
			jen.For(jen.List(jen.Id("name"), jen.Id("property")).Op(":=").Range().Id("schema").Dot("Properties")).Block(
				jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.Id("name"), jen.Id("property").Dot("Description")),
			),
			jen.Return(jen.Qual("strings", "Join").Call(jen.Id("parts"), jen.Lit(" "))),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchNormalizeText").Params(jen.Id("text").String()).String().
		Block(
			jen.Id("text").Op("=").Qual("strings", "ToLower").Call(jen.Id("text")),
			jen.Id("replacer").Op(":=").Qual("strings", "NewReplacer").Call(
				jen.Lit("_"), jen.Lit(" "),
				jen.Lit("-"), jen.Lit(" "),
				jen.Lit("."), jen.Lit(" "),
				jen.Lit("/"), jen.Lit(" "),
				jen.Lit(":"), jen.Lit(" "),
				jen.Lit(","), jen.Lit(" "),
				jen.Lit(";"), jen.Lit(" "),
				jen.Lit("("), jen.Lit(" "),
				jen.Lit(")"), jen.Lit(" "),
				jen.Lit("{"), jen.Lit(" "),
				jen.Lit("}"), jen.Lit(" "),
				jen.Lit("["), jen.Lit(" "),
				jen.Lit("]"), jen.Lit(" "),
				jen.Lit(`"`), jen.Lit(" "),
				jen.Lit("'"), jen.Lit(" "),
			),
			jen.Return(jen.Id("replacer").Dot("Replace").Call(jen.Id("text"))),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchToken").Params(jen.Id("token").String()).String().
		Block(
			jen.Id("token").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("token")),
			jen.If(jen.Len(jen.Id("token")).Op(">").Lit(3).Op("&&").Qual("strings", "HasSuffix").Call(jen.Id("token"), jen.Lit("s"))).Block(
				jen.Id("token").Op("=").Qual("strings", "TrimSuffix").Call(jen.Id("token"), jen.Lit("s")),
			),
			jen.Return(jen.Id("token")),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchStopWord").Params(jen.Id("token").String()).Bool().
		Block(
			jen.Switch(jen.Id("token")).Block(
				jen.Case(
					jen.Lit("a"), jen.Lit("an"), jen.Lit("and"), jen.Lit("by"), jen.Lit("for"), jen.Lit("from"),
					jen.Lit("get"), jen.Lit("in"), jen.Lit("into"), jen.Lit("list"), jen.Lit("of"), jen.Lit("or"),
					jen.Lit("please"), jen.Lit("review"), jen.Lit("show"), jen.Lit("the"), jen.Lit("this"), jen.Lit("to"),
					jen.Lit("with"),
				).Block(jen.Return(jen.True())),
				jen.Default().Block(jen.Return(jen.False())),
			),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchTokens").Params(jen.Id("text").String()).Index().String().
		Block(
			jen.Id("fields").Op(":=").Qual("strings", "Fields").Call(jen.Id("toolSearchNormalizeText").Call(jen.Id("text"))),
			jen.Id("tokens").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("fields"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("field")).Op(":=").Range().Id("fields")).Block(
				jen.Id("token").Op(":=").Id("toolSearchToken").Call(jen.Id("field")),
				jen.If(jen.Len(jen.Id("token")).Op("<=").Lit(1).Op("||").Id("toolSearchStopWord").Call(jen.Id("token"))).Block(jen.Continue()),
				jen.Id("tokens").Op("=").Append(jen.Id("tokens"), jen.Id("token")),
			),
			jen.Return(jen.Id("tokens")),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchTokenMatch").Params(jen.Id("a").String(), jen.Id("b").String()).Bool().
		Block(
			jen.Return(
				jen.Id("a").Op("==").Id("b").Op("||").
					Qual("strings", "HasPrefix").Call(jen.Id("a"), jen.Id("b")).Op("||").
					Qual("strings", "HasPrefix").Call(jen.Id("b"), jen.Id("a")),
			),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchTokenOverlap").Params(jen.Id("tool").Op("*").Id("ToolInfo"), jen.Id("query").String()).Params(jen.Int(), jen.Int()).
		Block(
			jen.Id("queryTokens").Op(":=").Id("toolSearchTokens").Call(jen.Id("query")),
			jen.If(jen.Len(jen.Id("queryTokens")).Op("==").Lit(0)).Block(jen.Return(jen.Lit(0), jen.Lit(0))),
			jen.Id("documentTokens").Op(":=").Id("toolSearchTokens").Call(jen.Id("toolSearchHaystack").Call(jen.Id("tool"))),
			jen.Return(jen.Id("toolSearchTokenOverlapForText").Call(jen.Id("queryTokens"), jen.Id("documentTokens")), jen.Len(jen.Id("queryTokens"))),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchTokenOverlapForText").Params(jen.Id("queryTokens").Index().String(), jen.Id("documentTokens").Index().String()).Int().
		Block(
			jen.Id("matched").Op(":=").Lit(0),
			jen.For(jen.List(jen.Id("_"), jen.Id("queryToken")).Op(":=").Range().Id("queryTokens")).Block(
				jen.For(jen.List(jen.Id("_"), jen.Id("documentToken")).Op(":=").Range().Id("documentTokens")).Block(
					jen.If(jen.Id("toolSearchTokenMatch").Call(jen.Id("queryToken"), jen.Id("documentToken"))).Block(
						jen.Id("matched").Op("++"),
						jen.Break(),
					),
				),
			),
			jen.Return(jen.Id("matched")),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchFuzzyScore").Params(jen.Id("query").String(), jen.Id("candidates").Index().String()).Int().
		Block(
			jen.Id("matches").Op(":=").Qual("github.com/sahilm/fuzzy", "Find").Call(jen.Id("query"), jen.Id("candidates")),
			jen.If(jen.Len(jen.Id("matches")).Op("==").Lit(0)).Block(jen.Return(jen.Lit(0))),
			jen.Return(jen.Id("matches").Index(jen.Lit(0)).Dot("Score")),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchWhyMatched").Params(jen.Id("tool").Op("*").Id("ToolInfo"), jen.Id("query").String(), jen.Id("score").Int(), jen.Id("settings").Id("toolSearchSettings")).Index().String().
		Block(
			jen.Id("query").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("query")),
			jen.If(jen.Id("query").Op("==").Lit("").Op("||").Id("score").Op("<").Lit(0)).Block(jen.Return(jen.Nil())),
			jen.Id("lowerQuery").Op(":=").Qual("strings", "ToLower").Call(jen.Id("query")),
			jen.Id("normalizedQuery").Op(":=").Qual("strings", "Join").Call(jen.Id("toolSearchTokens").Call(jen.Id("query")), jen.Lit(" ")),
			jen.Id("name").Op(":=").Qual("strings", "ToLower").Call(jen.Id("tool").Dot("Name")),
			jen.Id("normalizedName").Op(":=").Qual("strings", "Join").Call(jen.Id("toolSearchTokens").Call(jen.Id("tool").Dot("Name")), jen.Lit(" ")),
			jen.If(jen.Id("name").Op("==").Id("lowerQuery").Op("||").Id("normalizedName").Op("==").Id("normalizedQuery")).Block(
				jen.Return(jen.Index().String().Values(jen.Lit("exact tool name match"))),
			),
			jen.If(jen.Id("tool").Dot("Title").Op("!=").Nil()).Block(
				jen.Id("title").Op(":=").Qual("strings", "ToLower").Call(jen.Op("*").Id("tool").Dot("Title")),
				jen.Id("normalizedTitle").Op(":=").Qual("strings", "Join").Call(jen.Id("toolSearchTokens").Call(jen.Op("*").Id("tool").Dot("Title")), jen.Lit(" ")),
				jen.If(jen.Id("title").Op("==").Id("lowerQuery").Op("||").Id("normalizedTitle").Op("==").Id("normalizedQuery")).Block(
					jen.Return(jen.Index().String().Values(jen.Lit("exact title match"))),
				),
				jen.If(jen.Qual("strings", "HasPrefix").Call(jen.Id("title"), jen.Id("lowerQuery")).Op("||").Qual("strings", "HasPrefix").Call(jen.Id("normalizedTitle"), jen.Id("normalizedQuery"))).Block(
					jen.Return(jen.Index().String().Values(jen.Lit("prefix title match"))),
				),
			),
			jen.If(jen.Qual("strings", "HasPrefix").Call(jen.Id("name"), jen.Id("lowerQuery")).Op("||").Qual("strings", "HasPrefix").Call(jen.Id("normalizedName"), jen.Id("normalizedQuery"))).Block(
				jen.Return(jen.Index().String().Values(jen.Lit("prefix tool name match"))),
			),
			jen.If(jen.Id("settings").Dot("fuzzyNameMatching").Op("&&").Id("score").Op(">=").Id("settings").Dot("fuzzyNameWeight").Op("*").Lit(10)).Block(
				jen.Return(jen.Index().String().Values(jen.Lit("fuzzy tool name/title match"))),
			),
			jen.Id("category").Op(",").Id("tags").Op(",").Id("keywords").Op(":=").Id("toolDiscoveryMetadata").Call(jen.Id("tool")),
			jen.Id("metadata").Op(":=").Id("category").Op("+").Lit(" ").Op("+").Qual("strings", "Join").Call(jen.Id("tags"), jen.Lit(" ")).Op("+").Lit(" ").Op("+").Qual("strings", "Join").Call(jen.Id("keywords"), jen.Lit(" ")),
			jen.Id("queryTokens").Op(":=").Id("toolSearchTokens").Call(jen.Id("query")),
			jen.If(jen.Id("toolSearchTokenOverlapForText").Call(jen.Id("queryTokens"), jen.Id("toolSearchTokens").Call(jen.Id("metadata"))).Op(">").Lit(0)).Block(
				jen.Return(jen.Index().String().Values(jen.Lit("matched discovery metadata token"))),
			),
			jen.If(jen.Id("tool").Dot("Description").Op("!=").Nil().Op("&&").Id("toolSearchTokenOverlapForText").Call(jen.Id("queryTokens"), jen.Id("toolSearchTokens").Call(jen.Op("*").Id("tool").Dot("Description"))).Op(">").Lit(0)).Block(
				jen.Return(jen.Index().String().Values(jen.Lit("matched description token"))),
			),
			jen.If(jen.Id("toolSearchTokenOverlapForText").Call(jen.Id("queryTokens"), jen.Id("toolSearchTokens").Call(jen.Id("toolInputParameterText").Call(jen.Id("tool")))).Op(">").Lit(0)).Block(
				jen.Return(jen.Index().String().Values(jen.Lit("matched parameter/schema token"))),
			),
			jen.List(jen.Id("matched"), jen.Id("total")).Op(":=").Id("toolSearchTokenOverlap").Call(jen.Id("tool"), jen.Id("query")),
			jen.If(jen.Id("total").Op(">").Lit(0).Op("&&").Id("matched").Op(">").Lit(0)).Block(
				jen.Return(jen.Index().String().Values(jen.Qual("fmt", "Sprintf").Call(jen.Lit("matched %d of %d query tokens from %q against tool name, title, description, metadata, or parameters"), jen.Id("matched"), jen.Id("total"), jen.Id("query")))),
			),
			jen.Return(jen.Index().String().Values(jen.Qual("fmt", "Sprintf").Call(jen.Lit("matched query %q against tool metadata"), jen.Id("query")))),
		)
	stmt.Line()

	stmt.Func().Id("marshalToolSearchJSON").Params(jen.Id("value").Any()).Params(jen.Index().Byte(), jen.Error()).
		Block(
			jen.Return(jen.Id("json").Dot("Marshal").Call(
				jen.Id("value"),
				jen.Id("json").Dot("Deterministic").Call(jen.True()),
				jen.Id("jsontext").Dot("EscapeForHTML").Call(jen.False()),
				jen.Id("jsontext").Dot("WithIndent").Call(jen.Lit("  ")),
			)),
		)
	stmt.Line()

	stmt.Func().Id("toolExampleValue").Params(jen.Id("property").Map(jen.String()).Any()).Any().
		Block(
			jen.Id("typ").Op(":=").Lit(""),
			jen.Switch(jen.Id("v").Op(":=").Id("property").Index(jen.Lit("type")).Assert(jen.Type())).Block(
				jen.Case(jen.String()).Block(jen.Id("typ").Op("=").Id("v")),
				jen.Case(jen.Index().Any()).Block(
					jen.If(jen.Len(jen.Id("v")).Op(">").Lit(0)).Block(
						jen.If(jen.Id("s").Op(",").Id("ok").Op(":=").Id("v").Index(jen.Lit(0)).Assert(jen.String()), jen.Id("ok")).Block(
							jen.Id("typ").Op("=").Id("s"),
						),
					),
				),
			),
			jen.Switch(jen.Id("typ")).Block(
				jen.Case(jen.Lit("boolean")).Block(jen.Return(jen.False())),
				jen.Case(jen.Lit("integer"), jen.Lit("number")).Block(jen.Return(jen.Lit(0))),
				jen.Case(jen.Lit("array")).Block(jen.Return(jen.Index().Any().Values())),
				jen.Case(jen.Lit("object")).Block(jen.Return(jen.Map(jen.String()).Any().Values())),
				jen.Default().Block(jen.Return(jen.Lit("<string>"))),
			),
		)
	stmt.Line()

	stmt.Func().Id("toolCallArgumentsExample").Params(jen.Id("tool").Op("*").Id("ToolInfo")).Map(jen.String()).Any().
		Block(
			jen.Id("example").Op(":=").Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("name"):      jen.Id("tool").Dot("Name"),
				jen.Lit("arguments"): jen.Map(jen.String()).Any().Values(),
			}),
			jen.Id("raw").Op(":=").Id("toolRawJSON").Call(jen.Id("tool").Dot("InputSchema")),
			jen.If(jen.Len(jen.Id("raw")).Op("==").Lit(0)).Block(jen.Return(jen.Id("example"))),
			jen.Var().Id("schema").Struct(
				jen.Id("Required").Index().String().Tag(map[string]string{"json": "required"}),
				jen.Id("Properties").Map(jen.String()).Map(jen.String()).Any().Tag(map[string]string{"json": "properties"}),
			),
			jen.If(jen.Id("json").Dot("Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("schema")).Op("!=").Nil()).Block(jen.Return(jen.Id("example"))),
			jen.Id("arguments").Op(":=").Map(jen.String()).Any().Values(),
			jen.For(jen.List(jen.Id("_"), jen.Id("name")).Op(":=").Range().Id("schema").Dot("Required")).Block(
				jen.Id("arguments").Index(jen.Id("name")).Op("=").Id("toolExampleValue").Call(jen.Id("schema").Dot("Properties").Index(jen.Id("name"))),
			),
			jen.For(jen.List(jen.Id("name"), jen.Id("value")).Op(":=").Range().Id("toolDiscoveryCallTemplateArguments").Call(jen.Id("tool"))).Block(
				jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("schema").Dot("Properties").Index(jen.Id("name")), jen.Op("!").Id("ok")).Block(jen.Continue()),
				jen.Id("arguments").Index(jen.Id("name")).Op("=").Id("value"),
			),
			jen.Id("example").Index(jen.Lit("arguments")).Op("=").Id("arguments"),
			jen.Return(jen.Id("example")),
		)
	stmt.Line()

	stmt.Func().Id("toolSearchRank").Params(jen.Id("tool").Op("*").Id("ToolInfo"), jen.Id("query").String(), jen.Id("settings").Id("toolSearchSettings")).Int().
		Block(
			jen.Id("query").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("query")),
			jen.If(jen.Id("query").Op("==").Lit("")).Block(jen.Return(jen.Lit(0))),
			jen.Id("lowerQuery").Op(":=").Qual("strings", "ToLower").Call(jen.Id("query")),
			jen.Id("normalizedQuery").Op(":=").Qual("strings", "Join").Call(jen.Id("toolSearchTokens").Call(jen.Id("query")), jen.Lit(" ")),
			jen.Id("name").Op(":=").Qual("strings", "ToLower").Call(jen.Id("tool").Dot("Name")),
			jen.Id("normalizedName").Op(":=").Qual("strings", "Join").Call(jen.Id("toolSearchTokens").Call(jen.Id("tool").Dot("Name")), jen.Lit(" ")),
			jen.Id("title").Op(":=").Lit(""),
			jen.Id("normalizedTitle").Op(":=").Lit(""),
			jen.If(jen.Id("tool").Dot("Title").Op("!=").Nil()).Block(
				jen.Id("title").Op("=").Qual("strings", "ToLower").Call(jen.Op("*").Id("tool").Dot("Title")),
				jen.Id("normalizedTitle").Op("=").Qual("strings", "Join").Call(jen.Id("toolSearchTokens").Call(jen.Op("*").Id("tool").Dot("Title")), jen.Lit(" ")),
			),
			jen.If(jen.Id("settings").Dot("exactMatchMode").Op("!=").Lit("off")).Block(
				jen.If(jen.Id("name").Op("==").Id("lowerQuery").Op("||").Id("normalizedName").Op("==").Id("normalizedQuery")).Block(
					jen.Return(jen.Id("settings").Dot("nameWeight").Op("*").Lit(10)),
				),
				jen.If(jen.Id("title").Op("!=").Lit("").Op("&&").Parens(jen.Id("title").Op("==").Id("lowerQuery").Op("||").Id("normalizedTitle").Op("==").Id("normalizedQuery"))).Block(
					jen.Return(jen.Id("settings").Dot("titleWeight").Op("*").Lit(10)),
				),
			),
			jen.If(jen.Qual("strings", "HasPrefix").Call(jen.Id("name"), jen.Id("lowerQuery")).Op("||").Qual("strings", "HasPrefix").Call(jen.Id("normalizedName"), jen.Id("normalizedQuery"))).Block(
				jen.Return(jen.Id("settings").Dot("nameWeight").Op("*").Lit(8)),
			),
			jen.If(jen.Id("title").Op("!=").Lit("").Op("&&").Parens(jen.Qual("strings", "HasPrefix").Call(jen.Id("title"), jen.Id("lowerQuery")).Op("||").Qual("strings", "HasPrefix").Call(jen.Id("normalizedTitle"), jen.Id("normalizedQuery")))).Block(
				jen.Return(jen.Id("settings").Dot("titleWeight").Op("*").Lit(8)),
			),
			jen.If(jen.Qual("strings", "Contains").Call(jen.Id("name"), jen.Id("lowerQuery")).Op("||").Qual("strings", "Contains").Call(jen.Id("normalizedName"), jen.Id("normalizedQuery"))).Block(
				jen.Return(jen.Id("settings").Dot("nameWeight").Op("*").Lit(7)),
			),
			jen.If(jen.Id("title").Op("!=").Lit("").Op("&&").Parens(jen.Qual("strings", "Contains").Call(jen.Id("title"), jen.Id("lowerQuery")).Op("||").Qual("strings", "Contains").Call(jen.Id("normalizedTitle"), jen.Id("normalizedQuery")))).Block(
				jen.Return(jen.Id("settings").Dot("titleWeight").Op("*").Lit(7)),
			),
			jen.If(jen.Id("settings").Dot("fuzzyNameMatching")).Block(
				jen.Id("candidates").Op(":=").Index().String().Values(jen.Id("tool").Dot("Name")),
				jen.If(jen.Id("tool").Dot("Title").Op("!=").Nil()).Block(
					jen.Id("candidates").Op("=").Append(jen.Id("candidates"), jen.Op("*").Id("tool").Dot("Title")),
				),
				jen.Id("fuzzyScore").Op(":=").Id("toolSearchFuzzyScore").Call(jen.Id("query"), jen.Id("candidates")),
				jen.If(jen.Id("fuzzyScore").Op(">").Lit(0)).Block(
					jen.Id("score").Op(":=").Id("settings").Dot("fuzzyNameWeight").Op("*").Lit(10).Op("+").Id("fuzzyScore"),
					jen.Return(jen.Id("score")),
				),
			),
			jen.If(jen.Op("!").Id("settings").Dot("broadFallback")).Block(jen.Return(jen.Lit(-1))),
			jen.Id("category").Op(",").Id("tags").Op(",").Id("keywords").Op(":=").Id("toolDiscoveryMetadata").Call(jen.Id("tool")),
			jen.Id("metadata").Op(":=").Qual("strings", "ToLower").Call(jen.Id("category").Op("+").Lit(" ").Op("+").Qual("strings", "Join").Call(jen.Id("tags"), jen.Lit(" ")).Op("+").Lit(" ").Op("+").Qual("strings", "Join").Call(jen.Id("keywords"), jen.Lit(" "))),
			jen.If(jen.Qual("strings", "Contains").Call(jen.Id("metadata"), jen.Id("lowerQuery"))).Block(jen.Return(jen.Id("settings").Dot("metadataWeight").Op("*").Lit(5))),
			jen.If(jen.Id("tool").Dot("Description").Op("!=").Nil().Op("&&").Qual("strings", "Contains").Call(jen.Qual("strings", "ToLower").Call(jen.Op("*").Id("tool").Dot("Description")), jen.Id("lowerQuery"))).Block(jen.Return(jen.Id("settings").Dot("descriptionWeight").Op("*").Lit(5))),
			jen.If(jen.Qual("strings", "Contains").Call(jen.Qual("strings", "ToLower").Call(jen.Id("toolInputParameterText").Call(jen.Id("tool"))), jen.Id("lowerQuery"))).Block(jen.Return(jen.Id("settings").Dot("parameterWeight").Op("*").Lit(5))),
			jen.If(jen.Qual("strings", "Contains").Call(jen.Qual("strings", "ToLower").Call(jen.Id("toolSearchHaystack").Call(jen.Id("tool"))), jen.Id("lowerQuery"))).Block(jen.Return(jen.Id("settings").Dot("descriptionWeight").Op("*").Lit(3))),
			jen.List(jen.Id("matchedTokens"), jen.Id("totalTokens")).Op(":=").Id("toolSearchTokenOverlap").Call(jen.Id("tool"), jen.Id("query")),
			jen.If(jen.Id("matchedTokens").Op(">").Lit(0).Op("&&").Parens(jen.Id("totalTokens").Op("==").Lit(1).Op("||").Id("matchedTokens").Op(">").Lit(1))).Block(
				jen.Id("averageBroadWeight").Op(":=").Parens(jen.Id("settings").Dot("metadataWeight").Op("+").Id("settings").Dot("descriptionWeight").Op("+").Id("settings").Dot("parameterWeight")).Op("/").Lit(3),
				jen.Id("score").Op(":=").Id("averageBroadWeight").Op("*").Id("matchedTokens").Op("/").Id("totalTokens"),
				jen.If(jen.Id("matchedTokens").Op("==").Id("totalTokens")).Block(
					jen.Id("score").Op("+=").Id("averageBroadWeight").Op("/").Lit(2),
				),
				jen.Return(jen.Id("score")),
			),
			jen.Return(jen.Lit(-1)),
		)
	stmt.Line()
}

func emitHandleSearchTools(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("handleSearchTools").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("p").Op("*").Id("ToolsCallPayload"),
		jen.Id("stream").Id("toolCallStream"),
	).Params(jen.Bool(), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.Var().Id("payload").Id("toolSearchPayload")
			g.List(jen.Id("arguments"), jen.Id("err")).Op(":=").Id("mcpJSONRaw").Call(jen.Id("p").Dot("Arguments"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.False(), jen.Id("err")))
			g.If(jen.Len(jen.Qual("bytes", "TrimSpace").Call(jen.Id("arguments"))).Op("==").Lit(0)).Block(
				jen.Id("arguments").Op("=").Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(`{}`))),
			)
			g.If(jen.Id("err").Op(":=").Id("decodeMCPPayloadStrict").Call(jen.Id("arguments"), jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Lit(`Provide {"query":"..."} or {"pattern":"..."} to search tools.`)))),
			)
			g.Id("query").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("payload").Dot("Query"))
			g.Id("pattern").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("payload").Dot("Pattern"))
			g.If(jen.Id("query").Op("!=").Lit("").Op("&&").Id("pattern").Op("!=").Lit("")).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("provide query or pattern, not both")), jen.Lit("invalid_params"), jen.Lit(`Provide either {"query":"..."} or {"pattern":"..."}.`)))),
			)
			g.Var().Id("re").Op("*").Qual("regexp", "Regexp")
			g.If(jen.Id("pattern").Op("!=").Lit("")).Block(
				jen.List(jen.Id("compiled"), jen.Id("err")).Op(":=").Qual("regexp", "Compile").Call(jen.Lit("(?i)").Op("+").Id("pattern")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Lit("Provide a valid regular expression pattern.")))),
				),
				jen.Id("re").Op("=").Id("compiled"),
			)
			g.Id("settings").Op(":=").Id("a").Dot("toolSearchSettings").Call()
			g.Id("limit").Op(":=").Id("settings").Dot("maxResults")
			g.If(jen.Id("payload").Dot("MaxResults").Op("!=").Nil().Op("&&").Op("*").Id("payload").Dot("MaxResults").Op(">").Lit(0)).Block(
				jen.Id("limit").Op("=").Op("*").Id("payload").Dot("MaxResults"),
			)
			g.Id("matches").Op(":=").Make(jen.Index().Id("toolSearchCandidate"), jen.Lit(0))
			g.For(jen.List(jen.Id("order"), jen.Id("tool")).Op(":=").Range().Id("a").Dot("generatedToolCatalog").Call()).Block(
				jen.If(jen.Id("tool").Op("==").Nil()).Block(jen.Continue()),
				jen.If(jen.Op("!").Id("toolMatchesCategory").Call(jen.Id("tool"), jen.Id("payload").Dot("Category"))).Block(jen.Continue()),
				jen.If(jen.Op("!").Id("toolMatchesTags").Call(jen.Id("tool"), jen.Id("payload").Dot("Tags"))).Block(jen.Continue()),
				jen.Id("haystack").Op(":=").Id("toolSearchHaystack").Call(jen.Id("tool")),
				jen.Id("matched").Op(":=").Id("query").Op("==").Lit("").Op("&&").Id("pattern").Op("==").Lit(""),
				jen.Id("score").Op(":=").Lit(0),
				jen.If(jen.Id("query").Op("!=").Lit("")).Block(
					jen.Id("score").Op("=").Id("toolSearchRank").Call(jen.Id("tool"), jen.Id("query"), jen.Id("settings")),
					jen.Id("matched").Op("=").Id("score").Op(">=").Id("settings").Dot("minScore"),
				),
				jen.If(jen.Id("re").Op("!=").Nil()).Block(
					jen.Id("matched").Op("=").Id("re").Dot("MatchString").Call(jen.Id("haystack")),
				),
				jen.If(jen.Id("matched")).Block(
					jen.Id("matches").Op("=").Append(jen.Id("matches"), jen.Id("toolSearchCandidate").Values(jen.Dict{
						jen.Id("tool"):  jen.Id("tool"),
						jen.Id("score"): jen.Id("score"),
						jen.Id("order"): jen.Id("order"),
					})),
				),
			)
			g.Qual("sort", "SliceStable").Call(jen.Id("matches"), jen.Func().Params(jen.Id("i").Int(), jen.Id("j").Int()).Bool().Block(
				jen.If(jen.Id("matches").Index(jen.Id("i")).Dot("score").Op("!=").Id("matches").Index(jen.Id("j")).Dot("score")).Block(
					jen.Return(jen.Id("matches").Index(jen.Id("i")).Dot("score").Op(">").Id("matches").Index(jen.Id("j")).Dot("score")),
				),
				jen.Return(jen.Id("matches").Index(jen.Id("i")).Dot("order").Op("<").Id("matches").Index(jen.Id("j")).Dot("order")),
			))
			g.Id("narrowThreshold").Op(":=").Id("settings").Dot("nameWeight").Op("*").Lit(7)
			g.If(jen.Id("titleThreshold").Op(":=").Id("settings").Dot("titleWeight").Op("*").Lit(7), jen.Id("titleThreshold").Op("<").Id("narrowThreshold")).Block(
				jen.Id("narrowThreshold").Op("=").Id("titleThreshold"),
			)
			g.If(jen.Id("query").Op("!=").Lit("").Op("&&").Id("settings").Dot("exactMatchMode").Op("==").Lit("narrow").Op("&&").Len(jen.Id("matches")).Op(">").Lit(0).Op("&&").Id("matches").Index(jen.Lit(0)).Dot("score").Op(">=").Id("narrowThreshold")).Block(
				jen.Id("filtered").Op(":=").Id("matches").Index(jen.Empty(), jen.Lit(0)),
				jen.For(jen.List(jen.Id("_"), jen.Id("match")).Op(":=").Range().Id("matches")).Block(
					jen.If(jen.Id("match").Dot("score").Op(">=").Id("narrowThreshold")).Block(
						jen.Id("filtered").Op("=").Append(jen.Id("filtered"), jen.Id("match")),
					),
				),
				jen.Id("matches").Op("=").Id("filtered"),
			)
			g.Id("totalMatches").Op(":=").Len(jen.Id("matches"))
			g.Id("truncated").Op(":=").Id("totalMatches").Op(">").Id("limit")
			g.If(jen.Id("truncated")).Block(
				jen.Id("matches").Op("=").Id("matches").Index(jen.Empty(), jen.Id("limit")),
			)
			g.Id("descriptors").Op(":=").Make(jen.Index().Id("toolSearchDescriptor"), jen.Lit(0), jen.Len(jen.Id("matches")))
			g.Id("lines").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("matches")).Op("+").Lit(1))
			g.Id("_").Op(",").Id("callName").Op(":=").Id("a").Dot("toolSearchNames").Call()
			g.Id("lines").Op("=").Append(jen.Id("lines"), jen.Qual("fmt", "Sprintf").Call(jen.Lit("Found %d of %d matching tool(s)."), jen.Len(jen.Id("matches")), jen.Id("totalMatches")))
			g.For(jen.List(jen.Id("_"), jen.Id("match")).Op(":=").Range().Id("matches")).Block(
				jen.Id("descriptor").Op(":=").Id("toolSearchDescriptorFor").Call(jen.Id("match").Dot("tool"), jen.Id("payload").Dot("IncludeSchemas"), jen.Id("callName"), jen.Id("query"), jen.Id("match").Dot("score"), jen.Id("settings")),
				jen.Id("descriptors").Op("=").Append(jen.Id("descriptors"), jen.Id("descriptor")),
				jen.Id("lines").Op("=").Append(jen.Id("lines"),
					jen.Qual("fmt", "Sprintf").Call(jen.Lit("Tool: %s"), jen.Id("descriptor").Dot("Name")),
					jen.Qual("fmt", "Sprintf").Call(jen.Lit("Call this tool through %s. Do not call %s directly."), jen.Id("callName"), jen.Id("descriptor").Dot("Name")),
					jen.Qual("fmt", "Sprintf").Call(jen.Lit("Tool: %s"), jen.Id("callName")),
					jen.Lit("Arguments:"),
					jen.Id("descriptor").Dot("CallToolJSON"),
				),
			)
			g.Id("result").Op(":=").Op("&").Id("toolSearchResult").Values(jen.Dict{
				jen.Id("Tools"):        jen.Id("descriptors"),
				jen.Id("TotalMatches"): jen.Id("totalMatches"),
				jen.Id("Truncated"):    jen.Id("truncated"),
				jen.Id("Query"):        jen.Id("query"),
				jen.Id("Pattern"):      jen.Id("pattern"),
			})
			g.List(jen.Id("structured"), jen.Id("err")).Op(":=").Id("json").Dot("Marshal").Call(jen.Id("result"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.False(), jen.Id("err")))
			g.Id("text").Op(":=").Qual("strings", "Join").Call(jen.Id("lines"), jen.Lit("\n"))
			g.Return(jen.False(), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
				jen.Id("Content"):           jen.Index().Op("*").Id("ContentItem").Values(jen.Id("buildContentItem").Call(jen.Id("a"), jen.Id("text"))),
				jen.Id("StructuredContent"): jen.Id("mcpJSONFromRaw").Call(jen.Id("structured")),
			})))
		})
	stmt.Line()
}

func emitHandleCallToolProxy(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("handleCallToolProxy").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("p").Op("*").Id("ToolsCallPayload"),
		jen.Id("stream").Id("toolCallStream"),
	).Params(jen.Bool(), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.List(jen.Id("rawArguments"), jen.Id("err")).Op(":=").Id("mcpJSONRaw").Call(jen.Id("p").Dot("Arguments"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.False(), jen.Id("err")))
			g.Var().Id("payload").Id("toolCallProxyPayload")
			g.If(jen.Id("err").Op(":=").Id("decodeMCPPayloadStrict").Call(jen.Id("rawArguments"), jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Lit(`Provide {"name":"tool_name","arguments":{...}} to call a discovered tool.`)))),
			)
			g.Id("payload").Dot("Name").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("payload").Dot("Name"))
			g.If(jen.Id("payload").Dot("Name").Op("==").Lit("")).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing required field: name")), jen.Lit("invalid_params"), jen.Lit(`Provide {"name":"tool_name","arguments":{...}} to call a discovered tool.`)))),
			)
			g.If(jen.Id("a").Dot("isToolSearchName").Call(jen.Id("payload").Dot("Name")).Op("||").Id("a").Dot("isToolCallProxyName").Call(jen.Id("payload").Dot("Name"))).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("call_tool cannot call synthetic tool %q"), jen.Id("payload").Dot("Name")), jen.Lit("invalid_params"), jen.Lit("Call one of the real tools returned by search_tools.")))),
			)
			g.If(jen.Op("!").Id("isGeneratedToolName").Call(jen.Id("payload").Dot("Name"))).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("unknown target tool %q"), jen.Id("payload").Dot("Name")), jen.Lit("invalid_params"), jen.Lit("Call one of the real tools returned by search_tools.")))),
			)
			g.Id("arguments").Op(":=").Id("payload").Dot("Arguments")
			g.If(jen.Len(jen.Qual("bytes", "TrimSpace").Call(jen.Id("arguments"))).Op("==").Lit(0)).Block(
				jen.Id("arguments").Op("=").Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(`{}`))),
			)
			g.Id("proxied").Op(":=").Op("&").Id("ToolsCallPayload").Values(jen.Dict{
				jen.Id("Name"):      jen.Id("payload").Dot("Name"),
				jen.Id("Arguments"): jen.Id("mcpJSONFromRaw").Call(jen.Id("arguments")),
			})
			g.Id("info").Op(":=").Id("a").Dot("toolCallInfo").Call(jen.Id("proxied"), jen.Id("arguments"))
			g.Id("handler").Op(":=").Id("a").Dot("wrapToolCallHandler").Call(jen.Id("info"), jen.Id("a").Dot("collectRealToolCall"))
			g.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("handler").Call(jen.Id("ctx"), jen.Id("proxied"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.False(), jen.Id("err")))
			g.Id("toolErr").Op(":=").Id("result").Op("!=").Nil().Op("&&").Id("result").Dot("IsError").Op("!=").Nil().Op("&&").Op("*").Id("result").Dot("IsError")
			g.Return(jen.Id("toolErr"), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Id("result")))
		})
	stmt.Line()
}

func emitToolStreamBridges(stmt *jen.Statement, data *AdapterData) {
	for _, tool := range data.Tools {
		if !tool.IsStreaming {
			continue
		}

		typeName := streamBridgeTypeName(tool)
		eventType := rawExpr(tool.StreamEventType)

		stmt.Type().Id(typeName).Struct(
			jen.Id("out").Id("toolCallStream"),
			jen.Id("adapter").Op("*").Id("MCPAdapter"),
		)
		stmt.Line()
		emitStreamBridgeSendMethod(stmt, typeName, eventType)
		stmt.Line()
		stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).
			Id("Close").Params().Error().Block(jen.Return(jen.Nil()))
		stmt.Line()
	}
}

func streamBridgeTypeName(tool *ToolAdapter) string {
	return codegen.Goify(tool.OriginalMethodName, true) + "StreamBridge"
}

func emitStreamBridgeSendMethod(stmt *jen.Statement, typeName string, eventType jen.Code) {
	stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).
		Id("Send").Params(jen.Id("ev").Add(eventType)).Error().
		Block(jen.Return(jen.Id("b").Dot("SendWithContext").Call(jen.Qual("context", "Background").Call(), jen.Id("ev"))))
	stmt.Line()
	stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).
		Id("SendWithContext").Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("ev").Add(eventType)).
		Error().
		Block(
			jen.List(jen.Id("s"), jen.Id("e")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(jen.Id("ctx"), jen.Id("goahttp").Dot("ResponseEncoder"), jen.Id("ev")),
			jen.If(jen.Id("e").Op("!=").Nil()).Block(
				jen.Return(jen.Id("e")),
			),
			jen.Return(jen.Id("b").Dot("out").Dot("Send").Call(jen.Id("ctx"), jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
				jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
					jen.Id("buildContentItem").Call(jen.Id("b").Dot("adapter"), jen.Id("s")),
				),
			}))),
		)
}

func emitToolsCall(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("ToolsCall").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ToolsCallPayload"),
		).
		Params(jen.Id("res").Op("*").Id("ToolsCallResult"), jen.Id("err").Error()).
		Block(
			jen.Id("attrs").Op(":=").Index().Qual("go.opentelemetry.io/otel/attribute", "KeyValue").Values(),
			jen.Var().Id("rawArguments").Id("jsontext").Dot("Value"),
			jen.If(jen.Id("p").Op("!=").Nil()).Block(
				jen.List(jen.Id("rawArguments"), jen.Id("err")).Op("=").Id("mcpJSONRaw").Call(jen.Id("p").Dot("Arguments")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			),
			jen.If(jen.Id("p").Op("!=").Nil().Op("&&").Id("p").Dot("Name").Op("!=").Lit("")).Block(
				jen.Id("attrs").Op("=").Append(
					jen.Id("attrs"),
					jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("mcp.tool"), jen.Id("p").Dot("Name")),
					jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("tool"), jen.Id("p").Dot("Name")),
				),
				jen.If(jen.Id("a").Dot("isToolCallProxyName").Call(jen.Id("p").Dot("Name"))).Block(
					jen.Var().Id("target").Id("toolCallProxyPayload"),
					jen.If(jen.Id("json").Dot("Unmarshal").Call(jen.Id("rawArguments"), jen.Op("&").Id("target")).Op("==").Nil().Op("&&").Qual("strings", "TrimSpace").Call(jen.Id("target").Dot("Name")).Op("!=").Lit("")).Block(
						jen.Id("attrs").Op("=").Append(jen.Id("attrs"), jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("mcp.target_tool"), jen.Qual("strings", "TrimSpace").Call(jen.Id("target").Dot("Name")))),
					),
				),
			),
			jen.Id("ctx").Op(",").Id("span").Op(",").Id("start").Op(",").Id("attrs").Op(":=").Id("a").Dot("startTelemetry").Call(jen.Id("ctx"), jen.Lit("tools/call"), jen.Id("attrs").Op("...")),
			jen.Id("toolErr").Op(":=").False(),
			jen.Defer().Func().Params().Block(
				jen.Id("a").Dot("finishTelemetry").Call(jen.Id("ctx"), jen.Id("span"), jen.Id("start"), jen.Id("attrs"), jen.Id("err"), jen.Id("toolErr")),
			).Call(),
			jen.Id("info").Op(":=").Id("a").Dot("toolCallInfo").Call(jen.Id("p"), jen.Id("rawArguments")),
			jen.Id("handler").Op(":=").Id("a").Dot("wrapToolCallHandler").Call(jen.Id("info"), jen.Id("a").Dot("collectToolsCall")),
			jen.Id("res").Op(",").Id("err").Op("=").Id("handler").Call(jen.Id("ctx"), jen.Id("p")),
			jen.If(jen.Id("res").Op("!=").Nil().Op("&&").Id("res").Dot("IsError").Op("!=").Nil()).Block(
				jen.Id("toolErr").Op("=").Op("*").Id("res").Dot("IsError"),
			),
			jen.Return(jen.Id("res"), jen.Id("err")),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("collectToolsCall").
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("p").Op("*").Id("ToolsCallPayload")).
		Params(jen.Op("*").Id("ToolsCallResult"), jen.Error()).
		Block(
			jen.Return(jen.Id("a").Dot("collectToolCall").Call(jen.Id("ctx"), jen.Id("p"), jen.Id("a").Dot("toolsCallHandler"))),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("collectRealToolCall").
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("p").Op("*").Id("ToolsCallPayload")).
		Params(jen.Op("*").Id("ToolsCallResult"), jen.Error()).
		Block(
			jen.Return(jen.Id("a").Dot("collectToolCall").Call(jen.Id("ctx"), jen.Id("p"), jen.Id("a").Dot("executeRealTool"))),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("collectToolCall").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ToolsCallPayload"),
			jen.Id("handler").Id("toolCallStreamHandler"),
		).
		Params(jen.Op("*").Id("ToolsCallResult"), jen.Error()).
		Block(
			jen.Id("stream").Op(":=").Id("newToolCallResultCollector").Call(jen.Id("a")),
			jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("handler").Call(jen.Id("ctx"), jen.Id("p"), jen.Id("stream")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			jen.Return(jen.Id("stream").Dot("result").Call(), jen.Nil()),
		)
	stmt.Line()
}

func emitToolsCallHandler(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("toolsCallHandler").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ToolsCallPayload"),
			jen.Id("stream").Id("toolCallStream"),
		).
		Params(jen.Bool(), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.False(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.Id("name").Op(":=").Lit("")
			g.If(jen.Id("p").Op("!=").Nil()).Block(
				jen.Id("name").Op("=").Id("p").Dot("Name"),
			)
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("tools/call"),
				jen.Lit("name"):   jen.Id("name"),
			}))
			g.If(jen.Id("a").Dot("isToolSearchName").Call(jen.Id("name"))).Block(
				jen.Return(jen.Id("a").Dot("handleSearchTools").Call(jen.Id("ctx"), jen.Id("p"), jen.Id("stream"))),
			)
			g.If(jen.Id("a").Dot("isToolCallProxyName").Call(jen.Id("name"))).Block(
				jen.Return(jen.Id("a").Dot("handleCallToolProxy").Call(jen.Id("ctx"), jen.Id("p"), jen.Id("stream"))),
			)
			g.If(jen.Id("a").Dot("toolSearchEnabled").Call().Op("&&").Id("isGeneratedToolName").Call(jen.Id("name")).Op("&&").Op("!").Id("a").Dot("isAlwaysVisibleToolName").Call(jen.Id("name"))).Block(
				jen.Return(jen.False(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Unknown tool: %s"), jen.Id("name"))),
			)
			g.Return(jen.Id("a").Dot("executeRealTool").Call(jen.Id("ctx"), jen.Id("p"), jen.Id("stream")))
		})
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("executeRealTool").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ToolsCallPayload"),
			jen.Id("stream").Id("toolCallStream"),
		).
		Params(jen.Bool(), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.List(jen.Id("arguments"), jen.Id("err")).Op(":=").Id("mcpJSONRaw").Call(jen.Id("p").Dot("Arguments"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.False(), jen.Id("err")))
			g.Id("arguments").Op("=").Qual("bytes", "TrimSpace").Call(jen.Id("arguments"))
			g.If(jen.Len(jen.Id("arguments")).Op("==").Lit(0).Op("||").Qual("bytes", "Equal").Call(jen.Id("arguments"), jen.Index().Byte().Call(jen.Lit("null")))).Block(
				jen.Id("arguments").Op("=").Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(`{}`))),
			)
			g.Switch(jen.Id("p").Dot("Name")).BlockFunc(func(sw *jen.Group) {
				for _, tool := range data.Tools {
					sw.Case(jen.Lit(tool.Name)).BlockFunc(func(caseg *jen.Group) {
						emitToolCase(caseg, tool)
					})
				}
				sw.Default().Block(
					jen.Return(jen.False(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Unknown tool: %s"), jen.Id("p").Dot("Name"))),
				)
			})
		})
	stmt.Line()
}

func emitToolCase(g *jen.Group, tool *ToolAdapter) {
	if tool.Projected != nil {
		emitProjectedToolCase(g, tool)
		return
	}
	if tool.HasPayload {
		g.Var().Id("payload").Add(rawExpr(tool.PayloadType))
		if len(tool.DefaultFields) > 0 || len(tool.RequiredFields) > 0 || len(tool.EnumFields) > 0 {
			g.List(jen.Id("rawFields"), jen.Id("err")).Op(":=").Id("decodeMCPPayloadFields").Call(jen.Id("arguments"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Id(toolRecoveryFuncName(tool)).Call(jen.Id("err"), jen.Id("arguments"))))),
			)
		}
		g.If(jen.Id("err").Op(":=").Id("decodeMCPPayloadStrict").Call(jen.Id("arguments"), jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Id(toolRecoveryFuncName(tool)).Call(jen.Id("err"), jen.Id("arguments"))))),
		)
		emitToolDefaultAssignments(g, tool)
		emitToolRequiredChecks(g, tool)
		emitToolEnumChecks(g, tool)
	}

	if tool.IsStreaming {
		g.Id("bridge").Op(":=").Op("&").Id(streamBridgeTypeName(tool)).Values(jen.Dict{
			jen.Id("out"):     jen.Id("stream"),
			jen.Id("adapter"): jen.Id("a"),
		})
		call := serviceMethodCall(tool, jen.Id("a").Dot("service"), jen.Id("ctx"), tool.HasPayload, jen.Id("payload"), true, jen.Id("bridge"))
		g.If(jen.Id("err").Op(":=").Add(call), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("err"))),
		)
		g.Return(jen.False(), jen.Nil())
		return
	}

	if tool.HasResult {
		call := serviceMethodCall(tool, jen.Id("a").Dot("service"), jen.Id("ctx"), tool.HasPayload, jen.Id("payload"), false, nil)
		g.List(jen.Id("result"), jen.Id("err")).Op(":=").Add(call)
		g.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("err"))),
		)
		if tool.ResultType == "string" {
			g.Id("s").Op(":=").String().Call(jen.Id("result"))
			g.Id("structuredContent").Op(":=").Id("jsontext").Dot("Value").Call(jen.Nil())
		} else {
			g.List(jen.Id("structuredContent"), jen.Id("serr")).Op(":=").Id("json").Dot("Marshal").Call(jen.Id("result"))
			g.If(jen.Id("serr").Op("!=").Nil()).Block(
				jen.Return(jen.False(), jen.Id("serr")),
			)
			g.Id("s").Op(":=").String().Call(jen.Id("structuredContent"))
		}
		g.Id("final").Op(":=").Op("&").Id("ToolsCallResult").Values(jen.Dict{
			jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
				jen.Id("buildContentItem").Call(jen.Id("a"), jen.Id("s")),
			),
			jen.Id("StructuredContent"): jen.Id("mcpJSONFromRaw").Call(jen.Id("structuredContent")),
		})
		g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
			jen.Lit("method"): jen.Lit("tools/call"),
			jen.Lit("name"):   jen.Id("p").Dot("Name"),
		}))
		g.Return(jen.False(), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Id("final")))
		return
	}

	call := serviceMethodCall(tool, jen.Id("a").Dot("service"), jen.Id("ctx"), tool.HasPayload, jen.Id("payload"), false, nil)
	g.If(jen.Id("err").Op(":=").Add(call), jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("err"))),
	)
	g.Id("ok").Op(":=").Id("stringPtr").Call(jen.Lit("OK"))
	g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
		jen.Lit("method"): jen.Lit("tools/call"),
		jen.Lit("name"):   jen.Id("p").Dot("Name"),
	}))
	g.Return(jen.False(), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
		jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
			jen.Op("&").Id("ContentItem").Values(jen.Dict{
				jen.Id("Type"): jen.Lit("text"),
				jen.Id("Text"): jen.Id("ok"),
			}),
		),
	})))
}

func emitToolDefaultAssignments(g *jen.Group, tool *ToolAdapter) {
	if len(tool.DefaultFields) == 0 {
		return
	}
	g.BlockFunc(func(block *jen.Group) {
		for _, field := range tool.DefaultFields {
			block.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("rawFields").Index(jen.Lit(field.Name)), jen.Op("!").Id("ok")).Block(
				jen.Id("payload").Dot(field.GoName).Op("=").Add(rawExpr(field.Literal)),
			)
		}
	})
}

func emitToolRequiredChecks(g *jen.Group, tool *ToolAdapter) {
	if len(tool.RequiredFields) == 0 {
		return
	}
	g.BlockFunc(func(block *jen.Group) {
		for _, field := range tool.RequiredFields {
			block.If(jen.Id("err").Op(":=").Id("validateMCPPayloadRequired").Call(jen.Id("rawFields"), jen.Lit(field.Name), jen.Lit(field.AllowsNull)), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Id(toolRecoveryFuncName(tool)).Call(jen.Id("err"), jen.Id("arguments"))))),
			)
		}
	})
}

func emitToolEnumChecks(g *jen.Group, tool *ToolAdapter) {
	if len(tool.EnumFields) == 0 {
		return
	}
	g.BlockFunc(func(block *jen.Group) {
		for _, field := range tool.EnumFields {
			args := make([]jen.Code, 0, 3+len(field.Values))
			args = append(args, jen.Id("rawFields"), jen.Lit(field.Name), jen.Lit(field.Pointer))
			for _, val := range field.Values {
				args = append(args, jen.Lit(val))
			}
			block.If(jen.Id("err").Op(":=").Id("validateMCPPayloadEnum").Call(args...), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Id(toolRecoveryFuncName(tool)).Call(jen.Id("err"), jen.Id("arguments"))))),
			)
		}
	})
}

func serviceMethodCall(tool *ToolAdapter, receiver *jen.Statement, ctx jen.Code, hasPayload bool, payload jen.Code, withStream bool, stream jen.Code) jen.Code {
	args := []jen.Code{ctx}
	if hasPayload {
		args = append(args, payload)
	}
	if withStream {
		args = append(args, stream)
	}
	return receiver.Dot(tool.OriginalMethodName).Call(args...)
}

func emitProjectedToolCase(g *jen.Group, tool *ToolAdapter) {
	projected := tool.Projected
	specs := jen.Id(projected.SpecsPackageName)
	// MCP tools/call arguments are optional; official clients omit the key
	// entirely. Normalize absent arguments to the empty JSON object so the
	// toolset dispatcher never receives empty raw bytes (which would fail or
	// panic downstream when decoded as a nil value).
	g.Id("args").Op(":=").Id("arguments")
	g.If(jen.Len(jen.Id("args")).Op("==").Lit(0)).Block(
		jen.Id("args").Op("=").Id("jsontext").Dot("Value").Call(jen.Lit("{}")),
	)
	g.Id("meta").Op(":=").Op("&").Id("agentruntime").Dot("ToolCallMeta").Values()
	if len(projected.InjectedFields) > 0 {
		g.List(jen.Id("verifiedMeta"), jen.Id("ok")).Op(":=").Id("mcpruntime").Dot("ProjectedToolCallMetaFromContext").Call(jen.Id("ctx"))
		g.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(
				jen.Id("ctx"),
				jen.Id("stream"),
				jen.Id("p").Dot("Name"),
				jen.Qual("fmt", "Errorf").Call(jen.Lit("projected tool %q with Inject requires verified request context"), jen.Id("p").Dot("Name")),
			)),
		)
		g.Id("meta").Op("=").Op("&").Id("verifiedMeta")
	}
	g.List(jen.Id("toolResult"), jen.Id("err")).Op(":=").Id(projected.SpecsPackageName).Dot(projected.DispatcherFuncName).Call(
		jen.Id("ctx"),
		jen.Id("meta"),
		jen.Id("args"),
		jen.Nil(),
		specs.Dot(projected.DispatchOptionsName).Values(jen.Dict{
			jen.Id("Call"): projectedServiceCallFunc(projected),
		}),
	)
	g.If(jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("err"))),
	)
	g.If(jen.Id("toolResult").Op("==").Nil()).Block(
		jen.Return(jen.False(), jen.Qual("fmt", "Errorf").Call(jen.Lit("projected tool %q returned nil result"), jen.Id("p").Dot("Name"))),
	)
	g.If(jen.Id("toolResult").Dot("Error").Op("!=").Nil()).Block(
		jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolResult").Dot("Error"))),
	)
	if tool.HasResult {
		if projected.HasBounds {
			g.List(jen.Id("structuredContent"), jen.Id("serr")).Op(":=").Id("agentruntime").Dot("EncodeCanonicalToolResult").Call(
				jen.Id(projected.SpecsPackageName).Dot(projected.SpecName),
				jen.Id("toolResult").Dot("Result"),
				jen.Id("toolResult").Dot("Bounds"),
			)
		} else {
			g.List(jen.Id("structuredContent"), jen.Id("serr")).Op(":=").Id("json").Dot("Marshal").Call(jen.Id("toolResult").Dot("Result"))
		}
		g.If(jen.Id("serr").Op("!=").Nil()).Block(
			jen.Return(jen.False(), jen.Id("serr")),
		)
		structuredContentValue := jen.Id("structuredContent")
		if projected.HasBounds {
			structuredContentValue = jen.Id("jsontext").Dot("Value").Call(jen.Id("structuredContent"))
		}
		g.Id("s").Op(":=").String().Call(jen.Id("structuredContent"))
		g.Id("final").Op(":=").Op("&").Id("ToolsCallResult").Values(jen.Dict{
			jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
				jen.Id("buildContentItem").Call(jen.Id("a"), jen.Id("s")),
			),
			jen.Id("StructuredContent"): jen.Id("mcpJSONFromRaw").Call(structuredContentValue),
		})
		g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
			jen.Lit("method"): jen.Lit("tools/call"),
			jen.Lit("name"):   jen.Id("p").Dot("Name"),
		}))
		g.Return(jen.False(), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Id("final")))
		return
	}
	g.Id("ok").Op(":=").Id("stringPtr").Call(jen.Lit("OK"))
	g.Return(jen.False(), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
		jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
			jen.Op("&").Id("ContentItem").Values(jen.Dict{
				jen.Id("Type"): jen.Lit("text"),
				jen.Id("Text"): jen.Id("ok"),
			}),
		),
	})))
}

func projectedServiceCallFunc(projected *ProjectedToolAdapter) jen.Code {
	call := jen.Id("a").Dot("service").Dot(projected.BoundMethod)
	if !projected.HasPayload {
		return jen.Func().
			Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("args").Any()).
			Params(jen.Any(), jen.Error()).
			Block(jen.Return(call.Call(jen.Id("ctx"))))
	}
	return jen.Func().
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("args").Any()).
		Params(jen.Any(), jen.Error()).
		Block(jen.Return(call.Call(jen.Id("ctx"), jen.Id("args").Assert(rawExpr(projected.MethodPayloadType)))))
}
