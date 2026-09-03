package codegen

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func adapterPromptsSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-prompts", func(stmt *jen.Statement) {
		if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
			return
		}

		stmt.Comment("Prompts handling").Line()
		emitPromptsGet(stmt, data)
	})
}

func adapterResourcesSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-resources", func(stmt *jen.Statement) {
		if len(data.Resources) == 0 && len(data.SkillDirectories) == 0 {
			return
		}
		stmt.Comment("Resources handling").Line()
		emitResourceStreamBridges(stmt, data)
		emitResourcesRead(stmt, data)
	})
}

func emitResourceStreamBridges(stmt *jen.Statement, data *AdapterData) {
	for _, resource := range data.Resources {
		if !resource.IsStreaming {
			continue
		}
		typeName := resourceStreamBridgeTypeName(resource)
		eventType := rawExpr(resource.StreamEventType)
		stmt.Type().Id(typeName).Struct(
			jen.Id("adapter").Op("*").Id("MCPAdapter"),
			jen.Id("uri").String(),
			jen.Id("mimeType").String(),
			jen.Id("contents").Index().Op("*").Id("ResourceContent"),
		)
		stmt.Line()
		stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).Id("Send").
			Params(jen.Id("event").Add(eventType)).Error().
			Block(jen.Return(jen.Id("b").Dot("SendWithContext").Call(jen.Qual("context", "Background").Call(), jen.Id("event"))))
		stmt.Line()
		stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).Id("SendWithContext").
			Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("event").Add(eventType)).Error().
			Block(
				jen.List(jen.Id("text"), jen.Id("err")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(jen.Id("ctx"), jen.Id("goahttp").Dot("ResponseEncoder"), jen.Id("event")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Id("err"))),
				jen.Id("b").Dot("contents").Op("=").Append(jen.Id("b").Dot("contents"), jen.Op("&").Id("ResourceContent").Values(jen.Dict{
					jen.Id("URI"):      jen.Id("b").Dot("uri"),
					jen.Id("MimeType"): jen.Id("stringPtr").Call(jen.Id("b").Dot("mimeType")),
					jen.Id("Text"):     jen.Op("&").Id("text"),
				})),
				jen.Return(jen.Nil()),
			)
		stmt.Line()
		stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).Id("Close").Params().Error().Block(jen.Return(jen.Nil()))
		stmt.Line()
	}
}

func resourceStreamBridgeTypeName(resource *ResourceAdapter) string {
	return codegen.Goify(resource.OriginalMethodName, true) + "ResourceStreamCollector"
}

func emitResourcesRead(stmt *jen.Statement, data *AdapterData) {
	resourcePayload := jen.Op("*").Id("ResourcesReadPayload")
	resourceResult := jen.Op("*").Id("ResourcesReadResult")
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id("ResourcesRead").Params(
		jen.Id("ctx").Qual("context", "Context"), jen.Id("p").Add(resourcePayload),
	).Params(resourceResult, jen.Error()).Block(
		jen.Return(jen.Id("sdkbridge").Dot("DispatchResource").Call(jen.Id("ctx"), jen.Id("p"),
			jen.Id("sdkbridge").Dot("ResourceDispatchConfig").Types(resourcePayload, resourceResult).Values(jen.Dict{
				jen.Id("Initialized"): jen.Id("a").Dot("isInitialized"),
				jen.Id("URI"): jen.Func().Params(jen.Id("payload").Add(resourcePayload)).String().Block(
					jen.If(jen.Id("payload").Op("==").Nil()).Block(jen.Return(jen.Lit(""))),
					jen.Return(jen.Id("payload").Dot("URI")),
				),
				jen.Id("Policy"):       jen.Id("a").Dot("resourcePolicy"),
				jen.Id("Resources"):    jen.Id("a").Dot("resourceOperations"),
				jen.Id("Log"):          jen.Id("a").Dot("log"),
				jen.Id("MapError"):     jen.Id("a").Dot("safeMCPError"),
				jen.Id("SkillSources"): skillSourcesValue(data),
				jen.Id("SkillResult"):  skillResultValue(data),
			}),
		)),
	)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id("resourceOperationDescriptors").Params().
		Index().Id("sdkbridge").Dot("ResourceOperation").Types(resourcePayload, resourceResult).
		Block(jen.Return(jen.Index().Id("sdkbridge").Dot("ResourceOperation").Types(resourcePayload, resourceResult).ValuesFunc(func(vals *jen.Group) {
			for _, resource := range data.Resources {
				vals.Values(jen.Dict{
					jen.Id("URI"):    jen.Lit(resource.URI),
					jen.Id("Handle"): resourceOperationHandler(resource),
				})
			}
		})))
	stmt.Line()
	if len(data.SkillDirectories) > 0 {
		emitSkillSources(stmt, data)
	}
}

func skillSourcesValue(data *AdapterData) jen.Code {
	if len(data.SkillDirectories) == 0 {
		return jen.Nil()
	}
	return jen.Id("skillSources").Call()
}

func skillResultValue(data *AdapterData) jen.Code {
	if len(data.SkillDirectories) == 0 {
		return jen.Nil()
	}
	return jen.Func().Params(jen.Id("content").Op("*").Id("mcpskills").Dot("Content")).Op("*").Id("ResourcesReadResult").Block(
		jen.Return(jen.Op("&").Id("ResourcesReadResult").Values(jen.Dict{
			jen.Id("Contents"): jen.Index().Op("*").Id("ResourceContent").Values(
				jen.Op("&").Id("ResourceContent").Values(jen.Dict{
					jen.Id("URI"):      jen.Id("content").Dot("URI"),
					jen.Id("MimeType"): jen.Id("stringPtr").Call(jen.Id("content").Dot("MimeType")),
					jen.Id("Text"):     jen.Id("content").Dot("Text"),
					jen.Id("Blob"):     jen.Id("content").Dot("Blob"),
					jen.Id("Meta"): jen.Id("loom").Dot("NullableValue").Types(jen.Any()).Call(
						jen.Id("mcpskills").Dot("MetadataMeta").Call(jen.Id("content").Dot("Metadata")),
					),
				}),
			),
		})),
	)
}

func resourceOperationHandler(resource *ResourceAdapter) jen.Code {
	payloadName := "_"
	if resource.HasPayload {
		payloadName = "request"
	}
	return jen.Func().Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id(payloadName).Op("*").Id("ResourcesReadPayload"),
		jen.Id("baseURI").String(),
	).Params(jen.Op("*").Id("ResourcesReadResult"), jen.Error()).BlockFunc(func(g *jen.Group) {
		if resource.HasPayload {
			g.List(jen.Id("args"), jen.Id("err")).Op(":=").Id("sdkbridge").Dot("ResourceQueryJSONTyped").Call(jen.Id("request").Dot("URI"), resourceQueryFieldsValue(resource))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("sdkbridge").Dot("InvalidClientInput").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Invalid resource request.")))))
			g.Id("httpRequest").Op(":=").Op("&").Qual("net/http", "Request").Values(jen.Dict{
				jen.Id("Body"): jen.Qual("io", "NopCloser").Call(jen.Qual("bytes", "NewReader").Call(jen.Id("args"))),
				jen.Id("Header"): jen.Qual("net/http", "Header").Values(jen.Dict{
					jen.Lit("Content-Type"): jen.Index().String().Values(jen.Lit("application/json")),
				}),
			})
			g.Var().Id("payload").Add(rawExpr(resource.PayloadType))
			g.If(jen.Id("err").Op(":=").Id("goahttp").Dot("RequestDecoder").Call(jen.Id("httpRequest")).Dot("Decode").Call(jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("sdkbridge").Dot("InvalidClientInput").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Invalid resource request.")))),
			)
		}
		if resource.IsStreaming {
			g.Id("collector").Op(":=").Op("&").Id(resourceStreamBridgeTypeName(resource)).Values(jen.Dict{
				jen.Id("adapter"): jen.Id("a"), jen.Id("uri"): jen.Id("baseURI"), jen.Id("mimeType"): jen.Lit(resource.MimeType),
			})
			args := []jen.Code{jen.Id("ctx")}
			if resource.HasPayload {
				args = append(args, jen.Id("payload"))
			}
			args = append(args, jen.Id("collector"))
			g.If(jen.Id("err").Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(args...), jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err")))
			g.Return(jen.Op("&").Id("ResourcesReadResult").Values(jen.Dict{jen.Id("Contents"): jen.Id("collector").Dot("contents")}), jen.Nil())
			return
		}
		if resource.HasResult {
			args := []jen.Code{jen.Id("ctx")}
			if resource.HasPayload {
				args = append(args, jen.Id("payload"))
			}
			g.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(args...)
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err")))
			g.List(jen.Id("text"), jen.Id("err")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(jen.Id("ctx"), jen.Id("goahttp").Dot("ResponseEncoder"), jen.Id("result"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err")))
			g.Return(resourceReadResult(jen.Id("baseURI"), jen.Lit(resource.MimeType), jen.Op("&").Id("text")), jen.Nil())
			return
		}
		args := []jen.Code{jen.Id("ctx")}
		if resource.HasPayload {
			args = append(args, jen.Id("payload"))
		}
		g.If(jen.Id("err").Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(args...), jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err")))
		g.Return(resourceReadResult(jen.Id("baseURI"), jen.Lit(resource.MimeType), jen.Id("stringPtr").Call(jen.Lit(`{"status":"success"}`))), jen.Nil())
	})
}

func resourceQueryFieldsValue(resource *ResourceAdapter) jen.Code {
	fields := jen.Dict{}
	for _, field := range resource.QueryFields {
		metadata := jen.Dict{}
		if field.FormatKind == resourceQueryFormatString {
			metadata[jen.Id("String")] = jen.True()
		}
		if field.Repeated {
			metadata[jen.Id("Repeated")] = jen.True()
		}
		if len(metadata) > 0 {
			fields[jen.Lit(field.QueryKey)] = jen.Values(metadata)
		}
	}
	if len(fields) == 0 {
		return jen.Nil()
	}
	return jen.Map(jen.String()).Id("mcpruntime").Dot("QueryField").Values(fields)
}

func resourceReadResult(uri, mimeType, text jen.Code) jen.Code {
	return jen.Op("&").Id("ResourcesReadResult").Values(jen.Dict{
		jen.Id("Contents"): jen.Index().Op("*").Id("ResourceContent").Values(
			jen.Op("&").Id("ResourceContent").Values(jen.Dict{
				jen.Id("URI"): uri, jen.Id("MimeType"): jen.Id("stringPtr").Call(mimeType), jen.Id("Text"): text,
			}),
		),
	})
}

func emitSkillSources(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("skillSources").Params().Index().Id("mcpskills").Dot("Source").Block(
		jen.Return(jen.Index().Id("mcpskills").Dot("Source").ValuesFunc(func(vals *jen.Group) {
			for _, dir := range data.SkillDirectories {
				vals.Values(jen.Dict{
					jen.Id("Root"): jen.Lit(dir.Root),
				})
			}
		})),
	)
	stmt.Line()
}

func emitPromptsGet(stmt *jen.Statement, data *AdapterData) {
	promptPayload := jen.Op("*").Id("PromptsGetPayload")
	promptResult := jen.Op("*").Id("PromptsGetResult")
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id("PromptsGet").Params(
		jen.Id("ctx").Qual("context", "Context"), jen.Id("p").Add(promptPayload),
	).Params(promptResult, jen.Error()).Block(
		jen.Return(jen.Id("sdkbridge").Dot("DispatchNamed").Call(jen.Id("ctx"), jen.Id("p"),
			jen.Id("sdkbridge").Dot("NamedDispatchConfig").Types(promptPayload, promptResult).Values(jen.Dict{
				jen.Id("Method"): jen.Lit("prompts/get"), jen.Id("Initialized"): jen.Id("a").Dot("isInitialized"),
				jen.Id("Name"): jen.Func().Params(jen.Id("payload").Add(promptPayload)).String().Block(
					jen.If(jen.Id("payload").Op("==").Nil()).Block(jen.Return(jen.Lit(""))), jen.Return(jen.Id("payload").Dot("Name")),
				),
				jen.Id("Operations"): jen.Id("a").Dot("promptOperations"), jen.Id("Log"): jen.Id("a").Dot("log"),
				jen.Id("MapError"): jen.Id("a").Dot("safeMCPError"), jen.Id("FailureCode"): jen.Lit("internal_error"),
				jen.Id("FailureMessage"): jen.Lit("Prompt retrieval failed."), jen.Id("MissingName"): jen.Lit("Missing prompt name"),
				jen.Id("UnknownName"): jen.Lit("Unknown prompt: %s"),
			}),
		)),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id("promptOperationDescriptors").Params().
		Index().Id("sdkbridge").Dot("NamedOperation").Types(promptPayload, promptResult).
		Block(jen.Return(jen.Index().Id("sdkbridge").Dot("NamedOperation").Types(promptPayload, promptResult).ValuesFunc(func(vals *jen.Group) {
			for _, prompt := range data.StaticPrompts {
				vals.Values(jen.Dict{jen.Id("Name"): jen.Lit(prompt.Name), jen.Id("Handle"): staticPromptHandler(prompt)})
			}
			for _, prompt := range data.DynamicPrompts {
				vals.Values(jen.Dict{jen.Id("Name"): jen.Lit(prompt.Name), jen.Id("Handle"): dynamicPromptHandler(prompt)})
			}
		})))
	stmt.Line()
}

func staticPromptHandler(prompt *StaticPromptAdapter) jen.Code {
	messages := make([]jen.Code, 0, len(prompt.Messages))
	for _, message := range prompt.Messages {
		messages = append(messages, jen.Op("&").Id("PromptMessage").Values(jen.Dict{
			jen.Id("Role"):    jen.Lit(message.Role),
			jen.Id("Content"): jen.Op("&").Id("MessageContent").Values(jen.Dict{jen.Id("Type"): jen.Lit("text"), jen.Id("Text"): jen.Id("stringPtr").Call(jen.Lit(message.Content))}),
		}))
	}
	return jen.Func().Params(jen.Id("_").Qual("context", "Context"), jen.Id("payload").Op("*").Id("PromptsGetPayload")).Params(jen.Op("*").Id("PromptsGetResult"), jen.Error()).Block(
		jen.List(jen.Id("arguments"), jen.Id("err")).Op(":=").Id("mcpJSONRaw").Call(jen.Id("payload").Dot("Arguments")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
		jen.If(jen.Id("a").Dot("promptProvider").Op("!=").Nil()).Block(
			jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("promptProvider").Dot("Get"+codegen.Goify(prompt.Name, true)+"Prompt").Call(jen.Id("arguments")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			jen.If(jen.Id("result").Op("!=").Nil()).Block(jen.Return(jen.Id("result"), jen.Nil())),
		),
		jen.Return(jen.Op("&").Id("PromptsGetResult").Values(jen.Dict{
			jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit(prompt.Description)),
			jen.Id("Messages"):    jen.Index().Op("*").Id("PromptMessage").Values(messages...),
		}), jen.Nil()),
	)
}

func dynamicPromptHandler(prompt *DynamicPromptAdapter) jen.Code {
	return jen.Func().Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("payload").Op("*").Id("PromptsGetPayload")).Params(jen.Op("*").Id("PromptsGetResult"), jen.Error()).BlockFunc(func(g *jen.Group) {
		g.List(jen.Id("arguments"), jen.Id("err")).Op(":=").Id("mcpJSONRaw").Call(jen.Id("payload").Dot("Arguments"))
		g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err")))
		hasRequired := false
		for _, argument := range prompt.Arguments {
			if argument.Required {
				hasRequired = true
				break
			}
		}
		if hasRequired {
			g.Var().Id("args").Map(jen.String()).Any()
			g.If(jen.Len(jen.Id("arguments")).Op(">").Lit(0)).Block(
				jen.If(jen.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(jen.Id("arguments"), jen.Op("&").Id("args")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("sdkbridge").Dot("InvalidClientInput").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Invalid prompt arguments.")))),
				),
			)
			for _, argument := range prompt.Arguments {
				if argument.Required {
					g.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("args").Index(jen.Lit(argument.Name)), jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Nil(), jen.Id("sdkbridge").Dot("InvalidClientInput").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing required argument: %s"), jen.Lit(argument.Name)))),
					)
				}
			}
		}
		g.If(jen.Id("a").Dot("promptProvider").Op("==").Nil()).Block(jen.Return(jen.Nil(), jen.Id("sdkbridge").Dot("InvalidClientInput").Call(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("No prompt provider configured for dynamic prompts")))))
		g.Return(jen.Id("a").Dot("promptProvider").Dot("Get"+codegen.Goify(prompt.Name, true)+"Prompt").Call(jen.Id("ctx"), jen.Id("arguments")))
	})
}

func iconSliceValue(icons []*IconData) jen.Code {
	if len(icons) == 0 {
		return nil
	}
	values := make([]jen.Code, 0, len(icons))
	for _, icon := range icons {
		if icon == nil {
			continue
		}
		dict := jen.Dict{
			jen.Id("Src"): jen.Lit(icon.Source),
		}
		if icon.MIMEType != "" {
			dict[jen.Id("MimeType")] = jen.Id("stringPtr").Call(jen.Lit(icon.MIMEType))
		}
		if len(icon.Sizes) > 0 {
			sizes := make([]jen.Code, 0, len(icon.Sizes))
			for _, size := range icon.Sizes {
				sizes = append(sizes, jen.Lit(size))
			}
			dict[jen.Id("Sizes")] = jen.Index().String().Values(sizes...)
		}
		if icon.Theme != "" {
			dict[jen.Id("Theme")] = jen.Id("stringPtr").Call(jen.Lit(icon.Theme))
		}
		values = append(values, jen.Op("&").Id("Icon").Values(dict))
	}
	if len(values) == 0 {
		return nil
	}
	return jen.Index().Op("*").Id("Icon").Values(values...)
}

func promptProviderSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-prompt-provider", func(stmt *jen.Statement) {
		if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
			return
		}
		stmt.Comment("PromptProvider defines the interface for providing prompt content.").Line()
		stmt.Comment("Users must implement this interface to provide actual prompt implementations.").Line()
		stmt.Type().Id("PromptProvider").InterfaceFunc(func(g *jen.Group) {
			for _, prompt := range data.StaticPrompts {
				g.Commentf("Get%sPrompt returns the content for the %s prompt.", codegen.Goify(prompt.Name, true), prompt.Name)
				g.Id("Get"+codegen.Goify(prompt.Name, true)+"Prompt").
					Params(jen.Id("arguments").Id("jsontext").Dot("Value")).
					Params(jen.Op("*").Id("PromptsGetResult"), jen.Error())
			}
			for _, prompt := range data.DynamicPrompts {
				g.Commentf("Get%sPrompt returns the dynamic content for the %s prompt.", codegen.Goify(prompt.Name, true), prompt.Name)
				g.Id("Get"+codegen.Goify(prompt.Name, true)+"Prompt").
					Params(
						jen.Id("ctx").Qual("context", "Context"),
						jen.Id("arguments").Id("jsontext").Dot("Value"),
					).
					Params(jen.Op("*").Id("PromptsGetResult"), jen.Error())
			}
		})
		stmt.Line()
		if !hasRuntimePrompts(data.StaticPrompts) {
			return
		}
		stmt.Comment("RegisterRuntimePrompts registers design-declared MCP prompts as runtime prompt specs.").Line()
		stmt.Func().Id("RegisterRuntimePrompts").
			Params(jen.Id("reg").Op("*").Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "Registry")).
			Params(jen.Error()).
			BlockFunc(func(g *jen.Group) {
				for _, p := range data.StaticPrompts {
					if p.RuntimePrompt == nil {
						continue
					}
					g.If(
						jen.Err().Op(":=").Id("reg").Dot("Register").Call(
							jen.Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "PromptSpec").Values(jen.Dict{
								jen.Id("ID"):          jen.Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "Ident").Call(jen.Lit(p.Name)),
								jen.Id("AgentID"):     jen.Lit(p.RuntimePrompt.AgentID),
								jen.Id("Role"):        promptRoleCode(p.RuntimePrompt.Role),
								jen.Id("Description"): jen.Lit(p.Description),
								jen.Id("Template"):    jen.Lit(p.RuntimePrompt.Template),
								jen.Id("Version"):     jen.Lit(p.RuntimePrompt.Version),
							}),
						),
						jen.Err().Op("!=").Nil(),
					).Block(
						jen.Return(jen.Err()),
					)
				}
				g.Return(jen.Nil())
			})
	})
}

func hasRuntimePrompts(prompts []*StaticPromptAdapter) bool {
	for _, p := range prompts {
		if p.RuntimePrompt != nil {
			return true
		}
	}
	return false
}

func promptRoleCode(role string) jen.Code {
	switch role {
	case "system":
		return jen.Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "PromptRoleSystem")
	case "user":
		return jen.Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "PromptRoleUser")
	case "tool":
		return jen.Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "PromptRoleTool")
	case "synthesis":
		return jen.Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "PromptRoleSynthesis")
	default:
		return jen.Qual("github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt", "PromptRole").Call(jen.Lit(role))
	}
}
