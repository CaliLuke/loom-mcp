package codegen

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func adapterBroadcastSection() codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-broadcast", func(stmt *jen.Statement) {
		stmt.Comment("Broadcaster and publish helpers for server-initiated events").Line()

		stmt.Comment("Publish sends an event to all event stream subscribers.").Line()
		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("Publish").
			Params(jen.Id("ev").Op("*").Id("EventsStreamResult")).
			Block(
				jen.If(jen.Id("a").Op("==").Nil().Op("||").Id("a").Dot("broadcaster").Op("==").Nil()).Block(
					jen.Return(),
				),
				jen.Id("a").Dot("broadcaster").Dot("Publish").Call(jen.Id("ev")),
			)
		stmt.Line()

		stmt.Comment("PublishSession sends an event to subscribers for one MCP session.").Line()
		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("PublishSession").
			Params(jen.Id("sessionID").String(), jen.Id("ev").Op("*").Id("EventsStreamResult")).
			Block(
				jen.If(jen.Id("a").Op("==").Nil().Op("||").Id("a").Dot("broadcaster").Op("==").Nil()).Block(
					jen.Return(),
				),
				jen.If(jen.Id("sessionID").Op("==").Lit("")).Block(
					jen.Id("a").Dot("broadcaster").Dot("Publish").Call(jen.Id("ev")),
					jen.Return(),
				),
				jen.If(jen.List(jen.Id("scoped"), jen.Id("ok")).Op(":=").Id("a").Dot("broadcaster").Assert(jen.Id("mcpruntime").Dot("SessionBroadcaster")), jen.Id("ok")).Block(
					jen.Id("scoped").Dot("PublishSession").Call(jen.Id("sessionID"), jen.Id("ev")),
				),
			)
		stmt.Line()

		stmt.Comment("PublishContext sends an event to subscribers for the MCP session in ctx.").Line()
		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("PublishContext").
			Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("ev").Op("*").Id("EventsStreamResult")).
			Block(
				jen.Id("a").Dot("PublishSession").Call(jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")), jen.Id("ev")),
			)
		stmt.Line()

		stmt.Comment("PublishStatus is a convenience to publish a status_update message.").Line()
		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("PublishStatus").
			Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("typ").String(),
				jen.Id("message").String(),
				jen.Id("data").Any(),
			).
			Block(
				jen.Id("n").Op(":=").Op("&").Id("mcpruntime").Dot("Notification").Values(jen.Dict{
					jen.Id("Type"):    jen.Id("typ"),
					jen.Id("Message"): jen.Op("&").Id("message"),
					jen.Id("Data"):    jen.Id("data"),
				}),
				jen.List(jen.Id("s"), jen.Id("err")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(
					jen.Id("ctx"),
					jen.Id("goahttp").Dot("ResponseEncoder"),
					jen.Id("n"),
				),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(),
				),
				jen.Id("a").Dot("PublishContext").Call(
					jen.Id("ctx"),
					jen.Op("&").Id("EventsStreamResult").Values(jen.Dict{
						jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
							jen.Id("buildContentItem").Call(jen.Id("a"), jen.Id("s")),
						),
					}),
				),
			)
		stmt.Line()
	})
}

func adapterNotificationsSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-notifications", func(stmt *jen.Statement) {
		stmt.Comment("Notifications and events stream").Line()

		if len(data.Notifications) > 0 {
			stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
				Id("NotifyStatusUpdate").
				Params(
					jen.Id("ctx").Qual("context", "Context"),
					jen.Id("p").Op("*").Id("SendNotificationPayload"),
				).
				Error().
				Block(
					jen.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
						jen.Return(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
					),
					jen.If(jen.Id("p").Op("==").Nil().Op("||").Id("p").Dot("Type").Op("==").Lit("")).Block(
						jen.Return(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing notification type"))),
					),
					jen.Id("n").Op(":=").Op("&").Id("mcpruntime").Dot("Notification").Values(jen.Dict{
						jen.Id("Type"):    jen.Id("p").Dot("Type"),
						jen.Id("Message"): jen.Id("p").Dot("Message"),
						jen.Id("Data"):    jen.Id("p").Dot("Data"),
					}),
					jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("method"):  jen.Lit("notify_status_update"),
						jen.Lit("type"):    jen.Id("n").Dot("Type"),
						jen.Lit("message"): jen.Id("n").Dot("Message"),
					})),
					jen.List(jen.Id("s"), jen.Id("err")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(
						jen.Id("ctx"),
						jen.Id("goahttp").Dot("ResponseEncoder"),
						jen.Id("n"),
					),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Id("err")),
					),
					jen.Id("ev").Op(":=").Op("&").Id("EventsStreamResult").Values(jen.Dict{
						jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
							jen.Id("buildContentItem").Call(jen.Id("a"), jen.Id("s")),
						),
					}),
					jen.Id("a").Dot("PublishContext").Call(jen.Id("ctx"), jen.Id("ev")),
					jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("method"): jen.Lit("notify_status_update"),
						jen.Lit("type"):   jen.Id("n").Dot("Type"),
					})),
					jen.Return(jen.Nil()),
				)
			stmt.Line()
		}

		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("EventsStream").
			Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("stream").Id("EventsStreamServerStream"),
			).
			Params(jen.Id("res").Op("*").Id("EventsStreamResult"), jen.Id("err").Error()).
			Block(
				jen.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
					jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("internal_error"), jen.Lit("Not initialized"))),
				),
				jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("method"):     jen.Lit("events/stream"),
					jen.Lit("session_id"): jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
				})),
				jen.List(jen.Id("sessionID")).Op(":=").Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
				jen.Var().Id("sub").Id("mcpruntime").Dot("Subscription"),
				jen.If(jen.List(jen.Id("scoped"), jen.Id("ok")).Op(":=").Id("a").Dot("broadcaster").Assert(jen.Id("mcpruntime").Dot("SessionBroadcaster")), jen.Id("ok").Op("&&").Id("sessionID").Op("!=").Lit("")).Block(
					jen.List(jen.Id("sub"), jen.Id("err")).Op("=").Id("scoped").Dot("SubscribeSession").Call(jen.Id("ctx"), jen.Id("sessionID")),
				).Else().Block(
					jen.List(jen.Id("sub"), jen.Id("err")).Op("=").Id("a").Dot("broadcaster").Dot("Subscribe").Call(jen.Id("ctx")),
				),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("internal_error"), jen.Lit("Failed to subscribe to events: %v"), jen.Id("err"))),
				),
				jen.Defer().Id("sub").Dot("Close").Call(),
				jen.For().Block(
					jen.Select().Block(
						jen.Case(jen.Op("<-").Id("ctx").Dot("Done").Call()).Block(
							jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
								jen.Lit("method"):     jen.Lit("events/stream"),
								jen.Lit("session_id"): jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
								jen.Lit("closed"):     jen.True(),
								jen.Lit("reason"):     jen.Id("ctx").Dot("Err").Call().Dot("Error").Call(),
							})),
							jen.Return(jen.Nil(), jen.Id("ctx").Dot("Err").Call()),
						),
						jen.Case(jen.List(jen.Id("ev"), jen.Id("ok")).Op(":=").Op("<-").Id("sub").Dot("C").Call()).Block(
							jen.If(jen.Op("!").Id("ok")).Block(
								jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
									jen.Lit("method"):     jen.Lit("events/stream"),
									jen.Lit("session_id"): jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
									jen.Lit("closed"):     jen.True(),
									jen.Lit("reason"):     jen.Lit("broadcaster_closed"),
								})),
								jen.Return(jen.Nil(), jen.Nil()),
							),
							jen.List(jen.Id("evt"), jen.Id("ok")).Op(":=").Id("ev").Assert(jen.Id("EventsStreamEvent")),
							jen.If(jen.Op("!").Id("ok")).Block(
								jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
									jen.Lit("method"):             jen.Lit("events/stream"),
									jen.Lit("session_id"):         jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
									jen.Lit("dropped_event_type"): jen.Qual("fmt", "Sprintf").Call(jen.Lit("%T"), jen.Id("ev")),
								})),
								jen.Continue(),
							),
							jen.If(jen.Id("err").Op(":=").Id("stream").Dot("Send").Call(jen.Id("ctx"), jen.Id("evt")), jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("internal_error"), jen.Lit("Failed to send event: %v"), jen.Id("err"))),
							),
							jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
								jen.Lit("method"):     jen.Lit("events/stream"),
								jen.Lit("session_id"): jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
								jen.Lit("event_type"): jen.Qual("fmt", "Sprintf").Call(jen.Lit("%T"), jen.Id("evt")),
							})),
						),
					),
				),
			)
		stmt.Line()
	})
}

func adapterSubscriptionsSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-subscriptions", func(stmt *jen.Statement) {
		if len(data.Subscriptions) == 0 {
			return
		}

		stmt.Comment("General subscriptions handling").Line()
		stmt.Add(subscriptionHandler("Subscribe", "SubscribePayload", "SubscribeResult", "subscribe"))
		stmt.Line()
		stmt.Add(subscriptionHandler("Unsubscribe", "UnsubscribePayload", "UnsubscribeResult", "unsubscribe"))
		stmt.Line()
	})
}

func subscriptionHandler(name, payloadType, resultType, method string) jen.Code {
	return jen.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id(name).
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id(payloadType),
		).
		Params(jen.Op("*").Id(resultType), jen.Error()).
		Block(
			jen.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			),
			jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit(method),
			})),
			jen.Id("res").Op(":=").Op("&").Id(resultType).Values(jen.Dict{
				jen.Id("Success"): jen.True(),
			}),
			jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit(method),
			})),
			jen.Return(jen.Id("res"), jen.Nil()),
		)
}

func adapterPromptsSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-prompts", func(stmt *jen.Statement) {
		if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
			return
		}

		stmt.Comment("Prompts handling").Line()
		emitPromptsList(stmt, data)
		emitPromptsGet(stmt, data)
	})
}

func adapterResourcesSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-adapter-resources", func(stmt *jen.Statement) {
		if len(data.Resources) == 0 && len(data.SkillDirectories) == 0 {
			return
		}
		stmt.Comment("Resources handling").Line()
		emitResourcesList(stmt, data)
		emitResourcesRead(stmt, data)
		emitAssertResourceURIAllowed(stmt)
		emitResourcesSubscribe(stmt, data)
		emitResourcesUnsubscribe(stmt, data)
	})
}

func emitResourcesList(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("ResourcesList").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ResourcesListPayload"),
		).
		Params(jen.Op("*").Id("ResourcesListResult"), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			emitUnsupportedListCursorCheck(g, "resources/list")
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("resources/list"),
			}))
			g.Id("resources").Op(":=").Index().Op("*").Id("ResourceInfo").ValuesFunc(func(vals *jen.Group) {
				for _, resource := range data.Resources {
					dict := jen.Dict{
						jen.Id("URI"):         jen.Lit(resource.URI),
						jen.Id("Name"):        jen.Id("stringPtr").Call(jen.Lit(resource.Name)),
						jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit(resource.Description)),
						jen.Id("MimeType"):    jen.Id("stringPtr").Call(jen.Lit(resource.MimeType)),
					}
					if icons := iconSliceValue(resource.Icons); icons != nil {
						dict[jen.Id("Icons")] = icons
					}
					vals.Add(jen.Op("&").Id("ResourceInfo").Values(dict))
				}
			})
			if len(data.SkillDirectories) > 0 {
				g.Id("skillSources").Op(":=").Id("skillSources").Call()
				g.List(jen.Id("skillResources"), jen.Err()).Op(":=").Id("mcpskills").Dot("List").Call(jen.Id("ctx"), jen.Id("skillSources"))
				g.If(jen.Err().Op("!=").Nil()).Block(
					jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("error"), jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("method"): jen.Lit("resources/list"),
						jen.Lit("error"):  jen.Err().Dot("Error").Call(),
					})),
					jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Err(), jen.Lit("internal_error"), jen.Lit("Unable to list skill resources."))),
				)
				g.For(jen.List(jen.Id("_"), jen.Id("resource")).Op(":=").Range().Id("skillResources")).Block(
					jen.Id("resources").Op("=").Append(jen.Id("resources"), jen.Op("&").Id("ResourceInfo").Values(jen.Dict{
						jen.Id("URI"):         jen.Id("resource").Dot("URI"),
						jen.Id("Name"):        jen.Id("stringPtr").Call(jen.Id("resource").Dot("Name")),
						jen.Id("Description"): jen.Id("stringPtr").Call(jen.Id("resource").Dot("Description")),
						jen.Id("MimeType"):    jen.Id("stringPtr").Call(jen.Id("resource").Dot("MimeType")),
						jen.Id("Meta"):        jen.Id("mcpskills").Dot("MetadataMeta").Call(jen.Id("resource").Dot("Metadata")),
					})),
				)
			}
			g.Id("res").Op(":=").Op("&").Id("ResourcesListResult").Values(jen.Dict{
				jen.Id("Resources"): jen.Id("resources"),
			})
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("resources/list"),
			}))
			g.Return(jen.Id("res"), jen.Nil())
		})
	stmt.Line()
}

func emitResourcesRead(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("ResourcesRead").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ResourcesReadPayload"),
		).
		Params(jen.Op("*").Id("ResourcesReadResult"), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("resources/read"),
				jen.Lit("uri"):    jen.Id("p").Dot("URI"),
			}))
			g.Id("baseURI").Op(":=").Id("p").Dot("URI")
			g.If(jen.Id("i").Op(":=").Qual("strings", "Index").Call(jen.Id("baseURI"), jen.Lit("?")), jen.Id("i").Op(">=").Lit(0)).Block(
				jen.Id("baseURI").Op("=").Id("baseURI").Index(jen.Lit(0), jen.Id("i")),
			)
			if len(data.SkillDirectories) > 0 {
				g.If(jen.Qual("strings", "HasPrefix").Call(jen.Id("baseURI"), jen.Lit("skill://"))).Block(
					jen.Id("skillSources").Op(":=").Id("skillSources").Call(),
					jen.List(jen.Id("skillResources"), jen.Err()).Op(":=").Id("mcpskills").Dot("List").Call(jen.Id("ctx"), jen.Id("skillSources")),
					jen.If(jen.Err().Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Err(), jen.Lit("internal_error"), jen.Lit("Unable to inspect skill resource policy."))),
					),
					jen.Id("skillNameToURI").Op(":=").Make(jen.Map(jen.String()).String(), jen.Len(jen.Id("skillResources"))),
					jen.For(jen.List(jen.Id("_"), jen.Id("resource")).Op(":=").Range().Id("skillResources")).Block(
						jen.Id("policyURI").Op(":=").Id("resource").Dot("URI"),
						jen.If(jen.Qual("strings", "HasSuffix").Call(jen.Id("policyURI"), jen.Lit("/SKILL.md"))).Block(
							jen.Id("policyURI").Op("=").Qual("strings", "TrimSuffix").Call(jen.Id("policyURI"), jen.Lit("SKILL.md")),
						),
						jen.Id("skillNameToURI").Index(jen.Id("resource").Dot("Name")).Op("=").Id("policyURI"),
					),
					jen.If(jen.Id("err").Op(":=").Id("a").Dot("assertResourceURIAllowed").Call(jen.Id("ctx"), jen.Id("p").Dot("URI"), jen.Id("skillNameToURI")), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Lit("Resource URI is not allowed."))),
					),
					jen.List(jen.Id("content"), jen.Err()).Op(":=").Id("mcpskills").Dot("Read").Call(jen.Id("ctx"), jen.Id("skillSources"), jen.Id("baseURI")),
					jen.If(jen.Err().Op("!=").Nil()).Block(
						jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("error"), jen.Map(jen.String()).Any().Values(jen.Dict{
							jen.Lit("method"): jen.Lit("resources/read"),
							jen.Lit("uri"):    jen.Id("baseURI"),
							jen.Lit("error"):  jen.Err().Dot("Error").Call(),
						})),
						jen.Id("code").Op(":=").Lit("internal_error"),
						jen.If(jen.Qual("errors", "Is").Call(jen.Err(), jen.Id("mcpskills").Dot("ErrInvalidURI"))).Block(
							jen.Id("code").Op("=").Lit("invalid_params"),
						).Else().If(jen.Qual("errors", "Is").Call(jen.Err(), jen.Id("mcpskills").Dot("ErrNotFound"))).Block(
							jen.Id("code").Op("=").Lit("resource_not_found"),
						),
						jen.Id("message").Op(":=").Qual("fmt", "Sprintf").Call(jen.Lit("Unable to read skill resource: %s"), jen.Id("baseURI")),
						jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(
							jen.Id("loom").Dot("PermanentError").Call(jen.Id("code"), jen.Lit("%s"), jen.Id("message")),
							jen.Id("code"),
							jen.Id("message"),
						)),
					),
					jen.Id("res").Op(":=").Op("&").Id("ResourcesReadResult").Values(jen.Dict{
						jen.Id("Contents"): jen.Index().Op("*").Id("ResourceContent").Values(
							jen.Op("&").Id("ResourceContent").Values(jen.Dict{
								jen.Id("URI"):      jen.Id("content").Dot("URI"),
								jen.Id("MimeType"): jen.Id("stringPtr").Call(jen.Id("content").Dot("MimeType")),
								jen.Id("Text"):     jen.Id("content").Dot("Text"),
								jen.Id("Blob"):     jen.Id("content").Dot("Blob"),
								jen.Id("Meta"):     jen.Id("mcpskills").Dot("MetadataMeta").Call(jen.Id("content").Dot("Metadata")),
							}),
						),
					}),
					jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("method"): jen.Lit("resources/read"),
						jen.Lit("uri"):    jen.Id("baseURI"),
					})),
					jen.Return(jen.Id("res"), jen.Nil()),
				)
			}
			g.Switch(jen.Id("baseURI")).BlockFunc(func(sw *jen.Group) {
				for _, resource := range data.Resources {
					sw.Case(jen.Lit(resource.URI)).BlockFunc(func(caseg *jen.Group) {
						caseg.If(jen.Id("err").Op(":=").Id("a").Dot("assertResourceURIAllowed").Call(jen.Id("ctx"), jen.Id("p").Dot("URI"), jen.Nil()), jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Lit("Resource URI is not allowed."))),
						)
						if resource.HasPayload {
							caseg.List(jen.Id("args"), jen.Id("aerr")).Op(":=").Id("parseQueryParamsToJSON").Call(jen.Id("p").Dot("URI"))
							caseg.If(jen.Id("aerr").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("aerr"), jen.Lit("invalid_params"), jen.Lit("Invalid resource request."))),
							)
							caseg.Id("req").Op(":=").Op("&").Qual("net/http", "Request").Values(jen.Dict{
								jen.Id("Body"): jen.Qual("io", "NopCloser").Call(jen.Qual("bytes", "NewReader").Call(jen.Id("args"))),
								jen.Id("Header"): jen.Qual("net/http", "Header").Values(jen.Dict{
									jen.Lit("Content-Type"): jen.Index().String().Values(jen.Lit("application/json")),
								}),
							})
							caseg.Var().Id("payload").Add(rawExpr(resource.PayloadType))
							caseg.If(jen.Id("err").Op(":=").Id("goahttp").Dot("RequestDecoder").Call(jen.Id("req")).Dot("Decode").Call(jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Lit("Invalid resource request."))),
							)
						}
						if resource.HasResult {
							if resource.HasPayload {
								caseg.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(jen.Id("ctx"), jen.Id("payload"))
							} else {
								caseg.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(jen.Id("ctx"))
							}
							caseg.If(jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("internal_error"), jen.Lit("Resource read failed."))),
							)
							caseg.List(jen.Id("s"), jen.Id("serr")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(jen.Id("ctx"), jen.Id("goahttp").Dot("ResponseEncoder"), jen.Id("result"))
							caseg.If(jen.Id("serr").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("serr"), jen.Lit("internal_error"), jen.Lit("Resource read failed."))),
							)
							caseg.Id("res").Op(":=").Op("&").Id("ResourcesReadResult").Values(jen.Dict{
								jen.Id("Contents"): jen.Index().Op("*").Id("ResourceContent").Values(
									jen.Op("&").Id("ResourceContent").Values(jen.Dict{
										jen.Id("URI"):      jen.Id("baseURI"),
										jen.Id("MimeType"): jen.Id("stringPtr").Call(jen.Lit(resource.MimeType)),
										jen.Id("Text"):     jen.Op("&").Id("s"),
									}),
								),
							})
							caseg.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
								jen.Lit("method"): jen.Lit("resources/read"),
								jen.Lit("uri"):    jen.Id("baseURI"),
							}))
							caseg.Return(jen.Id("res"), jen.Nil())
							return
						}
						if resource.HasPayload {
							caseg.If(jen.Id("err").Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(jen.Id("ctx"), jen.Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("internal_error"), jen.Lit("Resource read failed."))),
							)
						} else {
							caseg.If(jen.Id("err").Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("internal_error"), jen.Lit("Resource read failed."))),
							)
						}
						caseg.Id("res").Op(":=").Op("&").Id("ResourcesReadResult").Values(jen.Dict{
							jen.Id("Contents"): jen.Index().Op("*").Id("ResourceContent").Values(
								jen.Op("&").Id("ResourceContent").Values(jen.Dict{
									jen.Id("URI"):      jen.Id("baseURI"),
									jen.Id("MimeType"): jen.Id("stringPtr").Call(jen.Lit(resource.MimeType)),
									jen.Id("Text"):     jen.Id("stringPtr").Call(jen.Lit(`{"status":"success"}`)),
								}),
							),
						})
						caseg.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
							jen.Lit("method"): jen.Lit("resources/read"),
							jen.Lit("uri"):    jen.Id("baseURI"),
						}))
						caseg.Return(jen.Id("res"), jen.Nil())
					})
				}
				sw.Default().Block(
					jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("resource_not_found"), jen.Lit("Unknown resource: %s"), jen.Id("p").Dot("URI"))),
				)
			})
		})
	stmt.Line()
	if len(data.SkillDirectories) > 0 {
		emitSkillSources(stmt, data)
	}
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

func emitAssertResourceURIAllowed(stmt *jen.Statement) {
	stmt.Comment("assertResourceURIAllowed verifies pURI passes allow/deny filters when configured.").Line()
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("assertResourceURIAllowed").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("pURI").String(),
			jen.Id("extraNameToURI").Map(jen.String()).String(),
		).
		Error().
		Block(
			jen.Id("base").Op(":=").Id("pURI"),
			jen.If(jen.Id("i").Op(":=").Qual("strings", "Index").Call(jen.Id("base"), jen.Lit("?")), jen.Id("i").Op(">=").Lit(0)).Block(
				jen.Id("base").Op("=").Id("base").Index(jen.Lit(0), jen.Id("i")),
			),
			jen.Var().Id("serverNameAllowURIs").Index().String(),
			jen.Var().Id("requestNameAllowURIs").Index().String(),
			jen.Var().Id("extraDenyURIs").Index().String(),
			jen.Var().Id("serverAllowedNames").Index().String(),
			jen.Var().Id("requestAllowedNames").Index().String(),
			jen.Var().Id("deniedNames").Index().String(),
			jen.If(jen.Id("a").Dot("opts").Op("!=").Nil()).Block(
				jen.Id("serverAllowedNames").Op("=").Append(jen.Id("serverAllowedNames"), jen.Id("a").Dot("opts").Dot("AllowedResourceNames").Op("...")),
				jen.Id("deniedNames").Op("=").Append(jen.Id("deniedNames"), jen.Id("a").Dot("opts").Dot("DeniedResourceNames").Op("...")),
			),
			jen.If(jen.Id("ctx").Op("!=").Nil()).Block(
				appendResourceNamesFromContextValue(jen.Id("mcpruntime").Dot("AllowedResourceNamesFromContext").Call(jen.Id("ctx")), "requestAllowedNames"),
				appendResourceNamesFromContextValue(jen.Id("mcpruntime").Dot("DeniedResourceNamesFromContext").Call(jen.Id("ctx")), "deniedNames"),
			),
			resolveNamedResourcePolicies("serverAllowedNames", "serverNameAllowURIs"),
			resolveNamedResourcePolicies("requestAllowedNames", "requestNameAllowURIs"),
			resolveNamedResourcePolicies("deniedNames", "extraDenyURIs"),
			jen.Var().Id("denied").Index().String(),
			jen.If(jen.Id("a").Dot("opts").Op("!=").Nil()).Block(
				jen.Id("denied").Op("=").Id("a").Dot("opts").Dot("DeniedResourceURIs"),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("d")).Op(":=").Range().Append(jen.Id("denied"), jen.Id("extraDenyURIs").Op("..."))).Block(
				jen.If(jen.Id("resourceURIMatchesPolicy").Call(jen.Id("base"), jen.Id("d"))).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("resource URI denied: %s"), jen.Id("pURI"))),
				),
			),
			jen.Var().Id("allowed").Index().String(),
			jen.If(jen.Id("a").Dot("opts").Op("!=").Nil()).Block(
				jen.Id("allowed").Op("=").Id("a").Dot("opts").Dot("AllowedResourceURIs"),
			),
			jen.Id("serverAllowConfigured").Op(":=").Len(jen.Id("allowed")).Op(">").Lit(0).Op("||").Len(jen.Id("serverAllowedNames")).Op(">").Lit(0),
			jen.Id("serverAllowPolicies").Op(":=").Append(jen.Index().String().Values(), jen.Id("allowed").Op("...")),
			jen.Id("serverAllowPolicies").Op("=").Append(jen.Id("serverAllowPolicies"), jen.Id("serverNameAllowURIs").Op("...")),
			jen.If(jen.Op("!").Id("resourceURIAllowedByPolicies").Call(jen.Id("base"), jen.Id("serverAllowConfigured"), jen.Id("serverAllowPolicies"))).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("resource URI not allowed: %s"), jen.Id("pURI"))),
			),
			jen.Id("requestAllowConfigured").Op(":=").Len(jen.Id("requestAllowedNames")).Op(">").Lit(0),
			jen.If(jen.Op("!").Id("resourceURIAllowedByPolicies").Call(jen.Id("base"), jen.Id("requestAllowConfigured"), jen.Id("requestNameAllowURIs"))).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("resource URI not allowed: %s"), jen.Id("pURI"))),
			),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Id("resourceURIAllowedByPolicies").Params(
		jen.Id("uri").String(),
		jen.Id("configured").Bool(),
		jen.Id("policies").Index().String(),
	).Bool().Block(
		jen.If(jen.Op("!").Id("configured")).Block(
			jen.Return(jen.True()),
		),
		jen.For(jen.List(jen.Id("_"), jen.Id("policy")).Op(":=").Range().Id("policies")).Block(
			jen.If(jen.Id("resourceURIMatchesPolicy").Call(jen.Id("uri"), jen.Id("policy"))).Block(
				jen.Return(jen.True()),
			),
		),
		jen.Return(jen.False()),
	)
	stmt.Line()
	stmt.Func().Id("resourceURIMatchesPolicy").Params(jen.Id("uri").String(), jen.Id("policy").String()).Bool().Block(
		jen.Id("policy").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("policy")),
		jen.Return(jen.Id("uri").Op("==").Id("policy").Op("||").Qual("strings", "HasSuffix").Call(jen.Id("policy"), jen.Lit("/")).Op("&&").Qual("strings", "HasPrefix").Call(jen.Id("uri"), jen.Id("policy"))),
	)
	stmt.Line()
}

func appendResourceNamesFromContextValue(resourceNames jen.Code, targetSlice string) jen.Code {
	return jen.If(jen.Id("s").Op(":=").Add(resourceNames), jen.Id("s").Op("!=").Lit("")).Block(
		jen.Id(targetSlice).Op("=").Append(jen.Id(targetSlice), jen.Qual("strings", "Split").Call(jen.Id("s"), jen.Lit(",")).Op("...")),
	)
}

func resolveNamedResourcePolicies(namesSlice, targetSlice string) jen.Code {
	return jen.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id(namesSlice)).Block(
		jen.Id("n").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("n")),
		jen.List(jen.Id("u"), jen.Id("ok")).Op(":=").Id("extraNameToURI").Index(jen.Id("n")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.List(jen.Id("u"), jen.Id("ok")).Op("=").Id("a").Dot("resourceNameToURI").Index(jen.Id("n")),
		),
		jen.If(jen.Id("ok")).Block(
			jen.Id(targetSlice).Op("=").Append(jen.Id(targetSlice), jen.Id("u")),
		),
	)
}

func emitResourcesSubscribe(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("ResourcesSubscribe").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ResourcesSubscribePayload"),
		).
		Error().
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.Switch(jen.Id("p").Dot("URI")).BlockFunc(func(sw *jen.Group) {
				for _, resource := range data.Resources {
					if !resource.Watchable {
						continue
					}
					sw.Case(jen.Lit(resource.URI)).Block(
						jen.Id("a").Dot("subsMu").Dot("Lock").Call(),
						jen.Id("a").Dot("subs").Index(jen.Id("p").Dot("URI")).Op("=").Id("a").Dot("subs").Index(jen.Id("p").Dot("URI")).Op("+").Lit(1),
						jen.Id("a").Dot("subsMu").Dot("Unlock").Call(),
						jen.Return(jen.Nil()),
					)
				}
				sw.Default().Block(
					jen.Return(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("resource_not_found"), jen.Lit("Unknown resource: %s"), jen.Id("p").Dot("URI"))),
				)
			})
		})
	stmt.Line()
}

func emitResourcesUnsubscribe(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("ResourcesUnsubscribe").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ResourcesUnsubscribePayload"),
		).
		Error().
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.Switch(jen.Id("p").Dot("URI")).BlockFunc(func(sw *jen.Group) {
				for _, resource := range data.Resources {
					if !resource.Watchable {
						continue
					}
					sw.Case(jen.Lit(resource.URI)).Block(
						jen.Id("a").Dot("subsMu").Dot("Lock").Call(),
						jen.If(jen.List(jen.Id("n"), jen.Id("ok")).Op(":=").Id("a").Dot("subs").Index(jen.Id("p").Dot("URI")), jen.Id("ok")).Block(
							jen.If(jen.Id("n").Op(">").Lit(1)).Block(
								jen.Id("a").Dot("subs").Index(jen.Id("p").Dot("URI")).Op("=").Id("n").Op("-").Lit(1),
							).Else().Block(
								jen.Delete(jen.Id("a").Dot("subs"), jen.Id("p").Dot("URI")),
							),
						),
						jen.Id("a").Dot("subsMu").Dot("Unlock").Call(),
						jen.Return(jen.Nil()),
					)
				}
				sw.Default().Block(
					jen.Return(jen.Id("loom").Dot("PermanentError").Call(jen.Lit("resource_not_found"), jen.Lit("Unknown resource: %s"), jen.Id("p").Dot("URI"))),
				)
			})
		})
	stmt.Line()
}

func emitPromptsList(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("PromptsList").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("PromptsListPayload"),
		).
		Params(jen.Op("*").Id("PromptsListResult"), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			emitUnsupportedListCursorCheck(g, "prompts/list")
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("prompts/list"),
			}))
			g.Id("prompts").Op(":=").Index().Op("*").Id("PromptInfo").ValuesFunc(func(vals *jen.Group) {
				for _, prompt := range data.DynamicPrompts {
					vals.Add(promptInfoValue(prompt.Name, prompt.Description, prompt.Icons, prompt.Arguments))
				}
				for _, prompt := range data.StaticPrompts {
					vals.Add(promptInfoValue(prompt.Name, prompt.Description, prompt.Icons, nil))
				}
			})
			g.Id("res").Op(":=").Op("&").Id("PromptsListResult").Values(jen.Dict{
				jen.Id("Prompts"): jen.Id("prompts"),
			})
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("prompts/list"),
			}))
			g.Return(jen.Id("res"), jen.Nil())
		})
	stmt.Line()
}

func emitUnsupportedListCursorCheck(g *jen.Group, method string) {
	g.If(
		jen.Id("p").Op("!=").Nil().Op("&&").
			Id("p").Dot("Cursor").Op("!=").Nil().Op("&&").
			Op("*").Id("p").Dot("Cursor").Op("!=").Lit(""),
	).Block(
		jen.Return(
			jen.Nil(),
			jen.Id("loom").Dot("PermanentError").Call(
				jen.Lit("invalid_params"),
				jen.Lit("%s pagination is not implemented; cursor must be empty"),
				jen.Lit(method),
			),
		),
	)
}

func emitPromptsGet(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("PromptsGet").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("PromptsGetPayload"),
		).
		Params(jen.Op("*").Id("PromptsGetResult"), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.If(jen.Id("p").Op("==").Nil().Op("||").Id("p").Dot("Name").Op("==").Lit("")).Block(
				jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing prompt name"))),
			)
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("prompts/get"),
				jen.Lit("name"):   jen.Id("p").Dot("Name"),
			}))
			if len(data.StaticPrompts) > 0 {
				g.Switch(jen.Id("p").Dot("Name")).BlockFunc(func(cases *jen.Group) {
					for _, prompt := range data.StaticPrompts {
						cases.Case(jen.Lit(prompt.Name)).Block(staticPromptCase(prompt)...)
					}
				})
			}
			if len(data.DynamicPrompts) > 0 {
				g.Switch(jen.Id("p").Dot("Name")).BlockFunc(func(cases *jen.Group) {
					for _, prompt := range data.DynamicPrompts {
						cases.Case(jen.Lit(prompt.Name)).Block(dynamicPromptCase(prompt)...)
					}
				})
			}
			g.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Unknown prompt: %s"), jen.Id("p").Dot("Name")))
		})
	stmt.Line()
}

func promptInfoValue(name, description string, icons []*IconData, args []PromptArg) jen.Code {
	dict := jen.Dict{
		jen.Id("Name"):        jen.Lit(name),
		jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit(description)),
	}
	if len(args) > 0 {
		argValues := make([]jen.Code, 0, len(args))
		for _, arg := range args {
			argValues = append(argValues, jen.Op("&").Id("PromptArgument").Values(jen.Dict{
				jen.Id("Name"):        jen.Lit(arg.Name),
				jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit(arg.Description)),
				jen.Id("Required"):    jen.Lit(arg.Required),
			}))
		}
		dict[jen.Id("Arguments")] = jen.Index().Op("*").Id("PromptArgument").Values(argValues...)
	}
	if iconsValue := iconSliceValue(icons); iconsValue != nil {
		dict[jen.Id("Icons")] = iconsValue
	}
	return jen.Op("&").Id("PromptInfo").Values(dict)
}

func staticPromptCase(prompt *StaticPromptAdapter) []jen.Code {
	codes := make([]jen.Code, 0, 6)
	codes = append(codes,
		jen.If(jen.Id("a").Dot("promptProvider").Op("!=").Nil()).Block(
			jen.If(jen.List(jen.Id("res"), jen.Id("err")).Op(":=").Id("a").Dot("promptProvider").Dot("Get"+codegen.Goify(prompt.Name, true)+"Prompt").Call(jen.Id("p").Dot("Arguments")), jen.Id("err").Op("==").Nil().Op("&&").Id("res").Op("!=").Nil()).Block(
				jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("method"): jen.Lit("prompts/get"),
					jen.Lit("name"):   jen.Id("p").Dot("Name"),
				})),
				jen.Return(jen.Id("res"), jen.Nil()),
			).Else().If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("internal_error"), jen.Lit("Prompt retrieval failed."))),
			),
		),
	)
	msgValues := make([]jen.Code, 0, len(prompt.Messages))
	for _, msg := range prompt.Messages {
		msgValues = append(msgValues, jen.Op("&").Id("PromptMessage").Values(jen.Dict{
			jen.Id("Role"): jen.Lit(msg.Role),
			jen.Id("Content"): jen.Op("&").Id("MessageContent").Values(jen.Dict{
				jen.Id("Type"): jen.Lit("text"),
				jen.Id("Text"): jen.Id("stringPtr").Call(jen.Lit(msg.Content)),
			}),
		}))
	}
	codes = append(codes,
		jen.Id("msgs").Op(":=").Index().Op("*").Id("PromptMessage").Values(msgValues...),
		jen.Id("res").Op(":=").Op("&").Id("PromptsGetResult").Values(jen.Dict{
			jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit(prompt.Description)),
			jen.Id("Messages"):    jen.Id("msgs"),
		}),
		jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
			jen.Lit("method"): jen.Lit("prompts/get"),
			jen.Lit("name"):   jen.Id("p").Dot("Name"),
		})),
		jen.Return(jen.Id("res"), jen.Nil()),
	)
	return codes
}

func dynamicPromptCase(prompt *DynamicPromptAdapter) []jen.Code {
	codes := make([]jen.Code, 0, 8)
	hasRequired := false
	for _, arg := range prompt.Arguments {
		if arg.Required {
			hasRequired = true
			break
		}
	}
	if hasRequired {
		codes = append(codes,
			jen.Var().Id("args").Map(jen.String()).Any(),
			jen.If(jen.Len(jen.Id("p").Dot("Arguments")).Op(">").Lit(0)).Block(
				jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("p").Dot("Arguments"), jen.Op("&").Id("args")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Lit("Invalid prompt arguments."))),
				),
			),
		)
		for _, arg := range prompt.Arguments {
			if arg.Required {
				codes = append(codes,
					jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("args").Index(jen.Lit(arg.Name)), jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing required argument: %s"), jen.Lit(arg.Name))),
					),
				)
			}
		}
	}
	codes = append(codes,
		jen.If(jen.Id("a").Dot("promptProvider").Op("==").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("loom").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("No prompt provider configured for dynamic prompts"))),
		),
		jen.List(jen.Id("res"), jen.Id("err")).Op(":=").Id("a").Dot("promptProvider").Dot("Get"+codegen.Goify(prompt.Name, true)+"Prompt").Call(jen.Id("ctx"), jen.Id("p").Dot("Arguments")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("a").Dot("safeMCPError").Call(jen.Id("err"), jen.Lit("internal_error"), jen.Lit("Prompt retrieval failed."))),
		),
		jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
			jen.Lit("method"): jen.Lit("prompts/get"),
			jen.Lit("name"):   jen.Id("p").Dot("Name"),
		})),
		jen.Return(jen.Id("res"), jen.Nil()),
	)
	return codes
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
					Params(jen.Id("arguments").Qual("encoding/json", "RawMessage")).
					Params(jen.Op("*").Id("PromptsGetResult"), jen.Error())
			}
			for _, prompt := range data.DynamicPrompts {
				g.Commentf("Get%sPrompt returns the dynamic content for the %s prompt.", codegen.Goify(prompt.Name, true), prompt.Name)
				g.Id("Get"+codegen.Goify(prompt.Name, true)+"Prompt").
					Params(
						jen.Id("ctx").Qual("context", "Context"),
						jen.Id("arguments").Qual("encoding/json", "RawMessage"),
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
