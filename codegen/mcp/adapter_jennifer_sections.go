package codegen

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func adapterBroadcastSection() codegen.Section {
	return codegen.MustJenniferSection("mcp-adapter-broadcast", func(stmt *jen.Statement) {
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
				jen.Id("a").Dot("Publish").Call(
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

func adapterNotificationsSection() codegen.Section {
	return codegen.MustJenniferSection("mcp-adapter-notifications", func(stmt *jen.Statement) {
		stmt.Comment("Notifications and events stream").Line()

		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("NotifyStatusUpdate").
			Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("n").Op("*").Id("mcpruntime").Dot("Notification"),
			).
			Error().
			Block(
				jen.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
					jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
				),
				jen.If(jen.Id("n").Op("==").Nil().Op("||").Id("n").Dot("Type").Op("==").Lit("")).Block(
					jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing notification type"))),
				),
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
				jen.Id("a").Dot("Publish").Call(jen.Id("ev")),
				jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("method"): jen.Lit("notify_status_update"),
					jen.Lit("type"):   jen.Id("n").Dot("Type"),
				})),
				jen.Return(jen.Nil()),
			)
		stmt.Line()

		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("EventsStream").
			Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("stream").Id("EventsStreamServerStream"),
			).
			Error().
			Block(
				jen.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
					jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("internal_error"), jen.Lit("Not initialized"))),
				),
				jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("method"):     jen.Lit("events/stream"),
					jen.Lit("session_id"): jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
				})),
				jen.List(jen.Id("sub"), jen.Id("err")).Op(":=").Id("a").Dot("broadcaster").Dot("Subscribe").Call(jen.Id("ctx")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("internal_error"), jen.Lit("Failed to subscribe to events: %v"), jen.Id("err"))),
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
							jen.Return(jen.Id("ctx").Dot("Err").Call()),
						),
						jen.Case(jen.List(jen.Id("ev"), jen.Id("ok")).Op(":=").Op("<-").Id("sub").Dot("C").Call()).Block(
							jen.If(jen.Op("!").Id("ok")).Block(
								jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
									jen.Lit("method"):     jen.Lit("events/stream"),
									jen.Lit("session_id"): jen.Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")),
									jen.Lit("closed"):     jen.True(),
									jen.Lit("reason"):     jen.Lit("broadcaster_closed"),
								})),
								jen.Return(jen.Nil()),
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
								jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("internal_error"), jen.Lit("Failed to send event: %v"), jen.Id("err"))),
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
	return codegen.MustJenniferSection("mcp-adapter-subscriptions", func(stmt *jen.Statement) {
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
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
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
	return codegen.MustJenniferSection("mcp-adapter-prompts", func(stmt *jen.Statement) {
		if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
			return
		}

		stmt.Comment("Prompts handling").Line()
		emitPromptsList(stmt, data)
		emitPromptsGet(stmt, data)
	})
}

func adapterResourcesSection(data *AdapterData) codegen.Section {
	return codegen.MustJenniferSection("mcp-adapter-resources", func(stmt *jen.Statement) {
		if len(data.Resources) == 0 {
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
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
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
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("resources/read"),
				jen.Lit("uri"):    jen.Id("p").Dot("URI"),
			}))
			g.Id("baseURI").Op(":=").Id("p").Dot("URI")
			g.If(jen.Id("i").Op(":=").Qual("strings", "Index").Call(jen.Id("baseURI"), jen.Lit("?")), jen.Id("i").Op(">=").Lit(0)).Block(
				jen.Id("baseURI").Op("=").Id("baseURI").Index(jen.Lit(0), jen.Id("i")),
			)
			g.Switch(jen.Id("baseURI")).BlockFunc(func(sw *jen.Group) {
				for _, resource := range data.Resources {
					sw.Case(jen.Lit(resource.URI)).BlockFunc(func(caseg *jen.Group) {
						caseg.If(jen.Id("err").Op(":=").Id("a").Dot("assertResourceURIAllowed").Call(jen.Id("ctx"), jen.Id("p").Dot("URI")), jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("%s"), jen.Id("err").Dot("Error").Call())),
						)
						if resource.HasPayload {
							caseg.List(jen.Id("args"), jen.Id("aerr")).Op(":=").Id("parseQueryParamsToJSON").Call(jen.Id("p").Dot("URI"))
							caseg.If(jen.Id("aerr").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("%s"), jen.Id("aerr").Dot("Error").Call())),
							)
							caseg.Id("req").Op(":=").Op("&").Qual("net/http", "Request").Values(jen.Dict{
								jen.Id("Body"): jen.Qual("io", "NopCloser").Call(jen.Qual("bytes", "NewReader").Call(jen.Id("args"))),
								jen.Id("Header"): jen.Qual("net/http", "Header").Values(jen.Dict{
									jen.Lit("Content-Type"): jen.Index().String().Values(jen.Lit("application/json")),
								}),
							})
							caseg.Var().Id("payload").Add(rawExpr(resource.PayloadType))
							caseg.If(jen.Id("err").Op(":=").Id("goahttp").Dot("RequestDecoder").Call(jen.Id("req")).Dot("Decode").Call(jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("%s"), jen.Id("err").Dot("Error").Call())),
							)
						}
						if resource.HasResult {
							if resource.HasPayload {
								caseg.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(jen.Id("ctx"), jen.Id("payload"))
							} else {
								caseg.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(jen.Id("ctx"))
							}
							caseg.If(jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("mapError").Call(jen.Id("err"))),
							)
							caseg.List(jen.Id("s"), jen.Id("serr")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(jen.Id("ctx"), jen.Id("goahttp").Dot("ResponseEncoder"), jen.Id("result"))
							caseg.If(jen.Id("serr").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("%s"), jen.Id("serr").Dot("Error").Call())),
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
								jen.Return(jen.Nil(), jen.Id("a").Dot("mapError").Call(jen.Id("err"))),
							)
						} else {
							caseg.If(jen.Id("err").Op(":=").Id("a").Dot("service").Dot(resource.OriginalMethodName).Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("a").Dot("mapError").Call(jen.Id("err"))),
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
					jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("method_not_found"), jen.Lit("Unknown resource: %s"), jen.Id("p").Dot("URI"))),
				)
			})
		})
	stmt.Line()
}

func emitAssertResourceURIAllowed(stmt *jen.Statement) {
	stmt.Comment("assertResourceURIAllowed verifies pURI passes allow/deny filters when configured.").Line()
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("assertResourceURIAllowed").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("pURI").String(),
		).
		Error().
		Block(
			jen.Id("base").Op(":=").Id("pURI"),
			jen.If(jen.Id("i").Op(":=").Qual("strings", "Index").Call(jen.Id("base"), jen.Lit("?")), jen.Id("i").Op(">=").Lit(0)).Block(
				jen.Id("base").Op("=").Id("base").Index(jen.Lit(0), jen.Id("i")),
			),
			jen.Var().Id("extraAllowURIs").Index().String(),
			jen.Var().Id("extraDenyURIs").Index().String(),
			jen.If(jen.Id("ctx").Op("!=").Nil()).Block(
				appendResourceURIsFromContextValue("mcp_allow_names", "extraAllowURIs"),
				appendResourceURIsFromContextValue("mcp_deny_names", "extraDenyURIs"),
			),
			jen.Var().Id("denied").Index().String(),
			jen.If(jen.Id("a").Dot("opts").Op("!=").Nil()).Block(
				jen.Id("denied").Op("=").Id("a").Dot("opts").Dot("DeniedResourceURIs"),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("d")).Op(":=").Range().Append(jen.Id("denied"), jen.Id("extraDenyURIs").Op("..."))).Block(
				jen.If(jen.Id("d").Op("==").Id("base")).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("resource URI denied: %s"), jen.Id("pURI"))),
				),
			),
			jen.Var().Id("allowed").Index().String(),
			jen.If(jen.Id("a").Dot("opts").Op("!=").Nil()).Block(
				jen.Id("allowed").Op("=").Id("a").Dot("opts").Dot("AllowedResourceURIs"),
			),
			jen.If(jen.Len(jen.Id("allowed")).Op("==").Lit(0).Op("&&").Len(jen.Id("extraAllowURIs")).Op("==").Lit(0)).Block(
				jen.Return(jen.Nil()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("allow")).Op(":=").Range().Append(jen.Id("allowed"), jen.Id("extraAllowURIs").Op("..."))).Block(
				jen.If(jen.Id("allow").Op("==").Id("base")).Block(
					jen.Return(jen.Nil()),
				),
			),
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("resource URI not allowed: %s"), jen.Id("pURI"))),
		)
	stmt.Line()
}

func appendResourceURIsFromContextValue(ctxKey, targetSlice string) jen.Code {
	return jen.If(jen.Id("v").Op(":=").Id("ctx").Dot("Value").Call(jen.Lit(ctxKey)), jen.Id("v").Op("!=").Nil()).Block(
		jen.If(jen.List(jen.Id("s"), jen.Id("ok")).Op(":=").Id("v").Assert(jen.String()), jen.Id("ok")).Block(
			jen.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Qual("strings", "Split").Call(jen.Id("s"), jen.Lit(","))).Block(
				jen.If(jen.List(jen.Id("u"), jen.Id("ok2")).Op(":=").Id("a").Dot("resourceNameToURI").Index(jen.Id("n")), jen.Id("ok2")).Block(
					jen.Id(targetSlice).Op("=").Append(jen.Id(targetSlice), jen.Id("u")),
				),
			),
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
				jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
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
					jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("method_not_found"), jen.Lit("Unknown resource: %s"), jen.Id("p").Dot("URI"))),
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
				jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
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
					jen.Return(jen.Id("goa").Dot("PermanentError").Call(jen.Lit("method_not_found"), jen.Lit("Unknown resource: %s"), jen.Id("p").Dot("URI"))),
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
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
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
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.If(jen.Id("p").Op("==").Nil().Op("||").Id("p").Dot("Name").Op("==").Lit("")).Block(
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing prompt name"))),
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
			g.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("method_not_found"), jen.Lit("Unknown prompt: %s"), jen.Id("p").Dot("Name")))
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
				jen.Return(jen.Nil(), jen.Id("err")),
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
					jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("%s"), jen.Id("err").Dot("Error").Call())),
				),
			),
		)
		for _, arg := range prompt.Arguments {
			if arg.Required {
				codes = append(codes,
					jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("args").Index(jen.Lit(arg.Name)), jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing required argument: "+arg.Name))),
					),
				)
			}
		}
	}
	codes = append(codes,
		jen.If(jen.Id("a").Dot("promptProvider").Op("==").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("No prompt provider configured for dynamic prompts"))),
		),
		jen.List(jen.Id("res"), jen.Id("err")).Op(":=").Id("a").Dot("promptProvider").Dot("Get"+codegen.Goify(prompt.Name, true)+"Prompt").Call(jen.Id("ctx"), jen.Id("p").Dot("Arguments")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("a").Dot("mapError").Call(jen.Id("err"))),
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
	return codegen.MustJenniferSection("mcp-prompt-provider", func(stmt *jen.Statement) {
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
	})
}
