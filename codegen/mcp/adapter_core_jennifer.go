package codegen

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func adapterCoreSection(data *AdapterData) codegen.Section {
	return codegen.MustJenniferSection("mcp-adapter-core", func(stmt *jen.Statement) {
		stmt.Comment("MCPAdapter core: types, options, constructor, helpers").Line()
		emitAdapterStruct(stmt, data)
		emitToolCallInterceptorTypes(stmt)
		emitAdapterOptions(stmt)
		emitAdapterConstructor(stmt, data)
		emitProtocolVersionHelpers(stmt)
		emitParseQueryParamsToJSON(stmt)
		emitSessionHelpers(stmt)
		emitLogAndMapError(stmt)
		emitToolCallInfoAndWrap(stmt, data)
		emitTelemetryHelpers(stmt, data)
		emitStringPtrAndBuildContentItem(stmt)
		emitSendToolError(stmt)
		emitFormatToolErrorText(stmt)
		emitToolCallError(stmt)
		emitToolInputError(stmt)
		emitInferToolInputRecovery(stmt)
		emitMissingFieldFromMessage(stmt)
		emitActionValueEnvelopeExample(stmt)
		emitFormatToolSuccessText(stmt)
		emitNormalizeToolSuccessValue(stmt)
		emitSummarizeToolSuccessValue(stmt)
		emitSummarizeToolSuccessList(stmt)
		emitSummarizeToolSuccessMap(stmt)
		emitScalarToolSuccessText(stmt)
		emitInitializeHandler(stmt, data)
		emitPingHandler(stmt)
	})
}

// emitAdapterStruct generates the MCPAdapter struct definition.
func emitAdapterStruct(stmt *jen.Statement, data *AdapterData) {
	stmt.Type().Id("MCPAdapter").StructFunc(func(g *jen.Group) {
		g.Id("service").Id(data.Package).Dot("Service")
		g.Id("initialized").Bool()
		g.Id("initializedSessions").Map(jen.String()).Struct()
		g.Id("sessionPrincipals").Map(jen.String()).String()
		g.Id("mu").Qual("sync", "RWMutex")
		g.Id("opts").Op("*").Id("MCPAdapterOptions")
		g.Id("tracer").Qual("go.opentelemetry.io/otel/trace", "Tracer")
		g.Id("callCounter").Qual("go.opentelemetry.io/otel/metric", "Int64Counter")
		g.Id("errorCounter").Qual("go.opentelemetry.io/otel/metric", "Int64Counter")
		g.Id("durationHistogram").Qual("go.opentelemetry.io/otel/metric", "Float64Histogram")
		if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
			g.Id("promptProvider").Id("PromptProvider")
		}
		g.Comment("Minimal subscription registry keyed by resource URI")
		g.Id("subs").Map(jen.String()).Int()
		g.Id("subsMu").Qual("sync", "Mutex")
		g.Comment("Broadcaster for server-initiated events (notifications/resources)")
		g.Id("broadcaster").Id("mcpruntime").Dot("Broadcaster")
		g.Comment("resourceNameToURI holds DSL-derived mapping for policy and lookups")
		g.Id("resourceNameToURI").Map(jen.String()).String()
	})
	stmt.Line()
}

func emitResolveNamedResourcePolicies(g *jen.Group, namesField, urisField, seenPrefix string) {
	g.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("opts").Dot(namesField)).Block(
		jen.If(jen.List(jen.Id("u"), jen.Id("ok")).Op(":=").Id("nameToURI").Index(jen.Id("n")), jen.Id("ok")).Block(
			jen.If(jen.List(jen.Id("_"), jen.Id("dup")).Op(":=").Id("seen").Index(jen.Lit(seenPrefix).Op("+").Id("u")), jen.Op("!").Id("dup")).Block(
				jen.Id("opts").Dot(urisField).Op("=").Append(jen.Id("opts").Dot(urisField), jen.Id("u")),
				jen.Id("seen").Index(jen.Lit(seenPrefix).Op("+").Id("u")).Op("=").Struct().Values(),
			),
		),
	)
}

// emitToolCallInterceptorTypes generates the interceptor-related types and impls.
func emitToolCallInterceptorTypes(stmt *jen.Statement) {
	stmt.Type().Defs(
		jen.Comment("ToolCallInterceptorInfo describes a generated MCP tools/call invocation.").Line().
			Id("ToolCallInterceptorInfo").Interface(
			jen.Id("goa").Dot("InterceptorInfo"),
			jen.Id("Tool").Params().String(),
			jen.Id("RawArguments").Params().Qual("encoding/json", "RawMessage"),
		),
		jen.Line().Comment("ToolCallHandler is the generated MCP tool-call dispatcher.").Line().
			Id("ToolCallHandler").Func().Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("payload").Op("*").Id("ToolsCallPayload"),
			jen.Id("stream").Id("ToolsCallServerStream"),
		).Params(jen.Bool(), jen.Error()),
		jen.Line().Comment("ToolCallInterceptor wraps generated MCP tool execution.").Line().
			Id("ToolCallInterceptor").Func().Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("info").Id("ToolCallInterceptorInfo"),
			jen.Id("payload").Op("*").Id("ToolsCallPayload"),
			jen.Id("stream").Id("ToolsCallServerStream"),
			jen.Id("next").Id("ToolCallHandler"),
		).Params(jen.Bool(), jen.Error()),
	)
	stmt.Line()

	// toolCallInterceptorInfo struct
	stmt.Type().Id("toolCallInterceptorInfo").Struct(
		jen.Id("service").String(),
		jen.Id("method").String(),
		jen.Id("tool").String(),
		jen.Id("rawPayload").Any(),
		jen.Id("rawArgs").Qual("encoding/json", "RawMessage"),
	)
	stmt.Line()

	// Method implementations
	for _, m := range []struct{ name, ret string }{
		{"Service", "i.service"},
		{"Method", "i.method"},
		{"Tool", "i.tool"},
	} {
		stmt.Func().Params(jen.Id("i").Op("*").Id("toolCallInterceptorInfo")).
			Id(m.name).Params().String().
			Block(jen.Return(jen.Id(m.ret)))
		stmt.Line()
	}

	stmt.Func().Params(jen.Id("i").Op("*").Id("toolCallInterceptorInfo")).
		Id("CallType").Params().Id("goa").Dot("InterceptorCallType").
		Block(jen.Return(jen.Id("goa").Dot("InterceptorUnary")))
	stmt.Line()

	stmt.Func().Params(jen.Id("i").Op("*").Id("toolCallInterceptorInfo")).
		Id("RawPayload").Params().Any().
		Block(jen.Return(jen.Id("i").Dot("rawPayload")))
	stmt.Line()

	stmt.Func().Params(jen.Id("i").Op("*").Id("toolCallInterceptorInfo")).
		Id("RawArguments").Params().Qual("encoding/json", "RawMessage").
		Block(jen.Return(jen.Id("i").Dot("rawArgs")))
	stmt.Line()
}

// emitAdapterOptions generates the MCPAdapterOptions struct.
func emitAdapterOptions(stmt *jen.Statement) {
	stmt.Comment("MCPAdapterOptions allows customizing adapter behavior.").Line()
	stmt.Type().Id("MCPAdapterOptions").Struct(
		jen.Comment("Logger is an optional hook called with internal adapter events."),
		jen.Id("Logger").Func().Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("event").String(), jen.Id("details").Any()),
		jen.Comment("ErrorMapper allows mapping arbitrary errors to framework-friendly errors"),
		jen.Id("ErrorMapper").Func().Params(jen.Error()).Error(),
		jen.Comment("ToolCallInterceptors wrap generated tools/call execution."),
		jen.Id("ToolCallInterceptors").Index().Id("ToolCallInterceptor"),
		jen.Comment("TelemetryName overrides the instrumentation scope used for OpenTelemetry spans and metrics."),
		jen.Id("TelemetryName").String(),
		jen.Comment("Tracer overrides the tracer used by the generated MCP adapter."),
		jen.Id("Tracer").Qual("go.opentelemetry.io/otel/trace", "Tracer"),
		jen.Comment("Meter overrides the meter used by the generated MCP adapter."),
		jen.Id("Meter").Qual("go.opentelemetry.io/otel/metric", "Meter"),
		jen.Comment("Allowed/Deny lists for resource URIs; Denied takes precedence unless header allow overrides"),
		jen.Id("AllowedResourceURIs").Index().String(),
		jen.Id("DeniedResourceURIs").Index().String(),
		jen.Comment("Name-based policy resolved to URIs at construction"),
		jen.Id("AllowedResourceNames").Index().String(),
		jen.Id("DeniedResourceNames").Index().String(),
		jen.Id("StructuredStreamJSON").Bool(),
		jen.Id("ProtocolVersionOverride").String(),
		jen.Comment("SessionPrincipal extracts a stable auth/session owner identity from ctx."),
		jen.Id("SessionPrincipal").Func().Params(jen.Qual("context", "Context")).String(),
		jen.Comment("Pluggable broadcaster, else default channel broadcaster"),
		jen.Id("Broadcaster").Id("mcpruntime").Dot("Broadcaster"),
		jen.Id("BroadcastBuffer").Int(),
		jen.Id("DropIfSlow").Bool(),
	)
	stmt.Line()
}

// emitAdapterConstructor generates NewMCPAdapter.
func emitAdapterConstructor(stmt *jen.Statement, data *AdapterData) {
	hasPrompts := len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0

	params := []jen.Code{
		jen.Id("service").Id(data.Package).Dot("Service"),
	}
	if hasPrompts {
		params = append(params, jen.Id("promptProvider").Id("PromptProvider"))
	}
	params = append(params, jen.Id("opts").Op("*").Id("MCPAdapterOptions"))

	stmt.Func().Id("NewMCPAdapter").Params(params...).Op("*").Id("MCPAdapter").BlockFunc(func(g *jen.Group) {
		// Resolve name-based policy to URIs
		g.Comment("Resolve name-based policy to URIs")
		g.If(jen.Id("opts").Op("!=").Nil().Op("&&").Parens(jen.Len(jen.Id("opts").Dot("AllowedResourceNames")).Op(">").Lit(0).Op("||").Len(jen.Id("opts").Dot("DeniedResourceNames")).Op(">").Lit(0))).BlockFunc(func(ig *jen.Group) {
			ig.Id("nameToURI").Op(":=").Map(jen.String()).String().ValuesFunc(func(vals *jen.Group) {
				for _, r := range data.Resources {
					vals.Lit(r.Name).Op(":").Lit(r.URI)
				}
			})
			ig.Id("seen").Op(":=").Map(jen.String()).Struct().Values()

			emitResolveNamedResourcePolicies(ig, "AllowedResourceNames", "AllowedResourceURIs", "allow:")
			emitResolveNamedResourcePolicies(ig, "DeniedResourceNames", "DeniedResourceURIs", "deny:")
		})

		// Broadcaster
		g.Comment("Broadcaster")
		g.Var().Id("bc").Id("mcpruntime").Dot("Broadcaster")
		g.If(jen.Id("opts").Op("!=").Nil().Op("&&").Id("opts").Dot("Broadcaster").Op("!=").Nil()).Block(
			jen.Id("bc").Op("=").Id("opts").Dot("Broadcaster"),
		).Else().BlockFunc(func(eg *jen.Group) {
			eg.Id("buf").Op(":=").Lit(32)
			eg.Id("drop").Op(":=").True()
			eg.If(jen.Id("opts").Op("!=").Nil()).Block(
				jen.If(jen.Id("opts").Dot("BroadcastBuffer").Op(">").Lit(0)).Block(
					jen.Id("buf").Op("=").Id("opts").Dot("BroadcastBuffer"),
				),
				jen.If(jen.Id("opts").Dot("DropIfSlow").Op("==").False()).Block(
					jen.Id("drop").Op("=").False(),
				),
			)
			eg.Id("bc").Op("=").Id("mcpruntime").Dot("NewChannelBroadcaster").Call(jen.Id("buf"), jen.Id("drop"))
		})

		// Telemetry
		g.Id("telemetryName").Op(":=").Id("defaultMCPAdapterTelemetryName").Call(jen.Id("opts"))
		g.Id("tracer").Op(":=").Id("defaultMCPAdapterTracer").Call(jen.Id("opts"), jen.Id("telemetryName"))
		g.List(jen.Id("callCounter"), jen.Id("errorCounter"), jen.Id("durationHistogram")).Op(":=").Id("defaultMCPAdapterMetrics").Call(jen.Id("opts"), jen.Id("telemetryName"))

		// Build name->URI map
		g.Comment("Build name->URI map from generated resources")
		g.Id("nameToURI").Op(":=").Map(jen.String()).String().ValuesFunc(func(vals *jen.Group) {
			for _, r := range data.Resources {
				vals.Lit(r.Name).Op(":").Lit(r.URI)
			}
		})

		// Return
		g.Return(jen.Op("&").Id("MCPAdapter").ValuesFunc(func(vals *jen.Group) {
			vals.Id("service").Op(":").Id("service")
			vals.Id("initializedSessions").Op(":").Make(jen.Map(jen.String()).Struct())
			vals.Id("sessionPrincipals").Op(":").Make(jen.Map(jen.String()).String())
			vals.Id("opts").Op(":").Id("opts")
			vals.Id("tracer").Op(":").Id("tracer")
			vals.Id("callCounter").Op(":").Id("callCounter")
			vals.Id("errorCounter").Op(":").Id("errorCounter")
			vals.Id("durationHistogram").Op(":").Id("durationHistogram")
			if hasPrompts {
				vals.Id("promptProvider").Op(":").Id("promptProvider")
			}
			vals.Id("subs").Op(":").Make(jen.Map(jen.String()).Int())
			vals.Id("broadcaster").Op(":").Id("bc")
			vals.Id("resourceNameToURI").Op(":").Id("nameToURI")
		}))
	})
	stmt.Line()
}

// emitProtocolVersionHelpers generates mcpProtocolVersion, supportsProtocolVersion, validMCPProtocolVersionDate.
func emitProtocolVersionHelpers(stmt *jen.Statement) {
	// mcpProtocolVersion
	stmt.Comment("mcpProtocolVersion resolves the protocol version from options or default.").Line()
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("mcpProtocolVersion").Params().String().
		Block(
			jen.If(jen.Id("a").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Dot("ProtocolVersionOverride").Op("!=").Lit("")).Block(
				jen.Return(jen.Id("a").Dot("opts").Dot("ProtocolVersionOverride")),
			),
			jen.Return(jen.Id("DefaultProtocolVersion")),
		)
	stmt.Line()

	// supportsProtocolVersion
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("supportsProtocolVersion").Params(jen.Id("requested").String()).Bool().
		Block(
			jen.Id("base").Op(":=").Id("a").Dot("mcpProtocolVersion").Call(),
			jen.If(jen.Id("requested").Op("==").Id("base")).Block(
				jen.Return(jen.True()),
			),
			jen.If(jen.Op("!").Id("validMCPProtocolVersionDate").Call(jen.Id("requested")).Op("||").Op("!").Id("validMCPProtocolVersionDate").Call(jen.Id("base"))).Block(
				jen.Return(jen.False()),
			),
			jen.Return(jen.Id("requested").Op(">=").Id("base")),
		)
	stmt.Line()

	// validMCPProtocolVersionDate
	stmt.Func().Id("validMCPProtocolVersionDate").Params(jen.Id("v").String()).Bool().
		Block(
			jen.If(jen.Len(jen.Id("v")).Op("!=").Lit(10)).Block(
				jen.Return(jen.False()),
			),
			jen.For(jen.Id("i").Op(":=").Range().Id("v")).Block(
				jen.Switch(jen.Id("i")).Block(
					jen.Case(jen.Lit(4), jen.Lit(7)).Block(
						jen.If(jen.Id("v").Index(jen.Id("i")).Op("!=").LitRune('-')).Block(
							jen.Return(jen.False()),
						),
					),
					jen.Default().Block(
						jen.If(jen.Id("v").Index(jen.Id("i")).Op("<").LitRune('0').Op("||").Id("v").Index(jen.Id("i")).Op(">").LitRune('9')).Block(
							jen.Return(jen.False()),
						),
					),
				),
			),
			jen.Return(jen.True()),
		)
	stmt.Line()
}

// emitParseQueryParamsToJSON generates the parseQueryParamsToJSON helper.
func emitParseQueryParamsToJSON(stmt *jen.Statement) {
	stmt.Comment("parseQueryParamsToJSON converts URI query params into JSON.").Line()
	stmt.Func().Id("parseQueryParamsToJSON").Params(jen.Id("uri").String()).Params(jen.Index().Byte(), jen.Error()).
		Block(
			jen.List(jen.Id("u"), jen.Id("err")).Op(":=").Qual("net/url", "Parse").Call(jen.Id("uri")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("invalid resource URI: %w"), jen.Id("err"))),
			),
			jen.Id("q").Op(":=").Id("u").Dot("Query").Call(),
			jen.If(jen.Len(jen.Id("q")).Op("==").Lit(0)).Block(
				jen.Return(jen.Index().Byte().Call(jen.Lit("{}")), jen.Nil()),
			),
			jen.Comment("Copy to plain map[string][]string to avoid depending on url.Values in helper"),
			jen.Id("m").Op(":=").Make(jen.Map(jen.String()).Index().String(), jen.Len(jen.Id("q"))),
			jen.For(jen.List(jen.Id("k"), jen.Id("v")).Op(":=").Range().Id("q")).Block(
				jen.Id("m").Index(jen.Id("k")).Op("=").Id("v"),
			),
			jen.Id("coerced").Op(":=").Id("mcpruntime").Dot("CoerceQuery").Call(jen.Id("m")),
			jen.Return(jen.Qual("encoding/json", "Marshal").Call(jen.Id("coerced"))),
		)
	stmt.Line()
}

// emitSessionHelpers generates session initialization and principal helpers.
func emitSessionHelpers(stmt *jen.Statement) {
	// isInitialized
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("isInitialized").Params(jen.Id("ctx").Qual("context", "Context")).Bool().
		Block(
			jen.Id("a").Dot("mu").Dot("RLock").Call(),
			jen.Defer().Id("a").Dot("mu").Dot("RUnlock").Call(),
			jen.If(jen.Id("sessionID").Op(":=").Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx")), jen.Id("sessionID").Op("!=").Lit("")).Block(
				jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("a").Dot("initializedSessions").Index(jen.Id("sessionID")),
				jen.Return(jen.Id("ok")),
			),
			jen.Return(jen.Id("a").Dot("initialized")),
		)
	stmt.Line()

	// markInitializedSession
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("markInitializedSession").Params(jen.Id("sessionID").String()).
		Block(
			jen.Id("a").Dot("mu").Dot("Lock").Call(),
			jen.Defer().Id("a").Dot("mu").Dot("Unlock").Call(),
			jen.If(jen.Id("sessionID").Op("==").Lit("")).Block(
				jen.Id("a").Dot("initialized").Op("=").True(),
				jen.Return(),
			),
			jen.Id("a").Dot("initializedSessions").Index(jen.Id("sessionID")).Op("=").Struct().Values(),
		)
	stmt.Line()

	// captureSessionPrincipal
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("captureSessionPrincipal").Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("sessionID").String()).
		Block(
			jen.If(jen.Id("a").Op("==").Nil().Op("||").Id("sessionID").Op("==").Lit("")).Block(jen.Return()),
			jen.Id("principal").Op(":=").Id("a").Dot("sessionPrincipal").Call(jen.Id("ctx")),
			jen.If(jen.Id("principal").Op("==").Lit("")).Block(jen.Return()),
			jen.Id("a").Dot("mu").Dot("Lock").Call(),
			jen.Defer().Id("a").Dot("mu").Dot("Unlock").Call(),
			jen.If(jen.Id("a").Dot("sessionPrincipals").Op("==").Nil()).Block(
				jen.Id("a").Dot("sessionPrincipals").Op("=").Make(jen.Map(jen.String()).String()),
			),
			jen.If(jen.Id("existing").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("a").Dot("sessionPrincipals").Index(jen.Id("sessionID"))), jen.Id("existing").Op("!=").Lit("")).Block(
				jen.Return(),
			),
			jen.Id("a").Dot("sessionPrincipals").Index(jen.Id("sessionID")).Op("=").Id("principal"),
		)
	stmt.Line()

	// clearSessionPrincipal
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("clearSessionPrincipal").Params(jen.Id("sessionID").String()).
		Block(
			jen.If(jen.Id("a").Op("==").Nil().Op("||").Id("sessionID").Op("==").Lit("")).Block(jen.Return()),
			jen.Id("a").Dot("mu").Dot("Lock").Call(),
			jen.Defer().Id("a").Dot("mu").Dot("Unlock").Call(),
			jen.Delete(jen.Id("a").Dot("sessionPrincipals"), jen.Id("sessionID")),
		)
	stmt.Line()

	// assertSessionPrincipal
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("assertSessionPrincipal").Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("sessionID").String()).Error().
		Block(
			jen.If(jen.Id("a").Op("==").Nil().Op("||").Id("sessionID").Op("==").Lit("")).Block(jen.Return(jen.Nil())),
			jen.Id("a").Dot("mu").Dot("RLock").Call(),
			jen.Id("expected").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("a").Dot("sessionPrincipals").Index(jen.Id("sessionID"))),
			jen.Id("a").Dot("mu").Dot("RUnlock").Call(),
			jen.If(jen.Id("expected").Op("==").Lit("")).Block(jen.Return(jen.Nil())),
			jen.Id("actual").Op(":=").Id("a").Dot("sessionPrincipal").Call(jen.Id("ctx")),
			jen.If(jen.Id("actual").Op("==").Lit("").Op("||").Id("actual").Op("!=").Id("expected")).Block(
				jen.Return(jen.Qual("errors", "New").Call(jen.Lit("session user mismatch"))),
			),
			jen.Return(jen.Nil()),
		)
	stmt.Line()

	// sessionPrincipal
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("sessionPrincipal").Params(jen.Id("ctx").Qual("context", "Context")).String().
		Block(
			jen.If(jen.Id("a").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Dot("SessionPrincipal").Op("!=").Nil()).Block(
				jen.Return(jen.Qual("strings", "TrimSpace").Call(jen.Id("a").Dot("opts").Dot("SessionPrincipal").Call(jen.Id("ctx")))),
			),
			jen.If(jen.Id("tokenInfo").Op(":=").Id("mcpauth").Dot("TokenInfoFromContext").Call(jen.Id("ctx")), jen.Id("tokenInfo").Op("!=").Nil()).Block(
				jen.Return(jen.Qual("strings", "TrimSpace").Call(jen.Id("tokenInfo").Dot("UserID"))),
			),
			jen.Return(jen.Lit("")),
		)
	stmt.Line()
}

// emitLogAndMapError generates the log and mapError helpers.
func emitLogAndMapError(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("log").Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("event").String(), jen.Id("details").Any()).
		Block(
			jen.If(jen.Id("a").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Dot("Logger").Op("!=").Nil()).Block(
				jen.Id("a").Dot("opts").Dot("Logger").Call(jen.Id("ctx"), jen.Id("event"), jen.Id("details")),
			),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("mapError").Params(jen.Id("err").Error()).Error().
		Block(
			jen.If(jen.Id("a").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Dot("ErrorMapper").Op("!=").Nil().Op("&&").Id("err").Op("!=").Nil()).Block(
				jen.If(jen.Id("m").Op(":=").Id("a").Dot("opts").Dot("ErrorMapper").Call(jen.Id("err")), jen.Id("m").Op("!=").Nil()).Block(
					jen.Return(jen.Id("m")),
				),
			),
			jen.Return(jen.Id("err")),
		)
	stmt.Line()
}

// emitToolCallInfoAndWrap generates toolCallInfo and wrapToolCallHandler.
func emitToolCallInfoAndWrap(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("toolCallInfo").Params(jen.Id("p").Op("*").Id("ToolsCallPayload")).Id("ToolCallInterceptorInfo").
		Block(
			jen.Id("info").Op(":=").Op("&").Id("toolCallInterceptorInfo").Values(jen.Dict{
				jen.Id("service"):    jen.Lit(data.ServiceName),
				jen.Id("method"):     jen.Lit("tools/call"),
				jen.Id("rawPayload"): jen.Id("p"),
			}),
			jen.If(jen.Id("p").Op("!=").Nil()).Block(
				jen.Id("info").Dot("tool").Op("=").Id("p").Dot("Name"),
				jen.Id("info").Dot("rawArgs").Op("=").Id("p").Dot("Arguments"),
			),
			jen.Return(jen.Id("info")),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("wrapToolCallHandler").Params(jen.Id("info").Id("ToolCallInterceptorInfo"), jen.Id("next").Id("ToolCallHandler")).Id("ToolCallHandler").
		Block(
			jen.If(jen.Id("a").Op("==").Nil().Op("||").Id("a").Dot("opts").Op("==").Nil().Op("||").Len(jen.Id("a").Dot("opts").Dot("ToolCallInterceptors")).Op("==").Lit(0)).Block(
				jen.Return(jen.Id("next")),
			),
			jen.Id("wrapped").Op(":=").Id("next"),
			jen.For(jen.Id("i").Op(":=").Len(jen.Id("a").Dot("opts").Dot("ToolCallInterceptors")).Op("-").Lit(1), jen.Id("i").Op(">=").Lit(0), jen.Id("i").Op("--")).Block(
				jen.Id("interceptor").Op(":=").Id("a").Dot("opts").Dot("ToolCallInterceptors").Index(jen.Id("i")),
				jen.If(jen.Id("interceptor").Op("==").Nil()).Block(jen.Continue()),
				jen.Id("currentNext").Op(":=").Id("wrapped"),
				jen.Id("wrapped").Op("=").Func().Params(
					jen.Id("ctx").Qual("context", "Context"),
					jen.Id("payload").Op("*").Id("ToolsCallPayload"),
					jen.Id("stream").Id("ToolsCallServerStream"),
				).Params(jen.Bool(), jen.Error()).Block(
					jen.Return(jen.Id("interceptor").Call(jen.Id("ctx"), jen.Id("info"), jen.Id("payload"), jen.Id("stream"), jen.Id("currentNext"))),
				),
			),
			jen.Return(jen.Id("wrapped")),
		)
	stmt.Line()
}

// emitTelemetryHelpers generates telemetry-related functions.
func emitTelemetryHelpers(stmt *jen.Statement, data *AdapterData) {
	// defaultMCPAdapterTelemetryName
	stmt.Func().Id("defaultMCPAdapterTelemetryName").Params(jen.Id("opts").Op("*").Id("MCPAdapterOptions")).String().
		Block(
			jen.If(jen.Id("opts").Op("!=").Nil().Op("&&").Id("opts").Dot("TelemetryName").Op("!=").Lit("")).Block(
				jen.Return(jen.Id("opts").Dot("TelemetryName")),
			),
			jen.Return(jen.Lit("loom-mcp/mcp/"+data.MCPPackage)),
		)
	stmt.Line()

	// defaultMCPAdapterTracer
	stmt.Func().Id("defaultMCPAdapterTracer").Params(jen.Id("opts").Op("*").Id("MCPAdapterOptions"), jen.Id("name").String()).Qual("go.opentelemetry.io/otel/trace", "Tracer").
		Block(
			jen.If(jen.Id("opts").Op("!=").Nil().Op("&&").Id("opts").Dot("Tracer").Op("!=").Nil()).Block(
				jen.Return(jen.Id("opts").Dot("Tracer")),
			),
			jen.Return(jen.Qual("go.opentelemetry.io/otel", "Tracer").Call(jen.Id("name"))),
		)
	stmt.Line()

	// defaultMCPAdapterMetrics
	stmt.Func().Id("defaultMCPAdapterMetrics").Params(
		jen.Id("opts").Op("*").Id("MCPAdapterOptions"),
		jen.Id("name").String(),
	).Params(
		jen.Qual("go.opentelemetry.io/otel/metric", "Int64Counter"),
		jen.Qual("go.opentelemetry.io/otel/metric", "Int64Counter"),
		jen.Qual("go.opentelemetry.io/otel/metric", "Float64Histogram"),
	).Block(
		jen.Var().Id("meter").Qual("go.opentelemetry.io/otel/metric", "Meter"),
		jen.If(jen.Id("opts").Op("!=").Nil().Op("&&").Id("opts").Dot("Meter").Op("!=").Nil()).Block(
			jen.Id("meter").Op("=").Id("opts").Dot("Meter"),
		).Else().Block(
			jen.Id("meter").Op("=").Qual("go.opentelemetry.io/otel", "Meter").Call(jen.Id("name")),
		),
		jen.List(jen.Id("callCounter"), jen.Id("_")).Op(":=").Id("meter").Dot("Int64Counter").Call(
			jen.Lit("loom_mcp.mcp.calls"),
			jen.Qual("go.opentelemetry.io/otel/metric", "WithUnit").Call(jen.Lit("{call}")),
			jen.Qual("go.opentelemetry.io/otel/metric", "WithDescription").Call(jen.Lit("Total MCP calls handled by the generated adapter.")),
		),
		jen.List(jen.Id("errorCounter"), jen.Id("_")).Op(":=").Id("meter").Dot("Int64Counter").Call(
			jen.Lit("loom_mcp.mcp.errors"),
			jen.Qual("go.opentelemetry.io/otel/metric", "WithUnit").Call(jen.Lit("{call}")),
			jen.Qual("go.opentelemetry.io/otel/metric", "WithDescription").Call(jen.Lit("Total MCP calls handled by the generated adapter that resulted in an error.")),
		),
		jen.List(jen.Id("durationHistogram"), jen.Id("_")).Op(":=").Id("meter").Dot("Float64Histogram").Call(
			jen.Lit("loom_mcp.mcp.duration_ms"),
			jen.Qual("go.opentelemetry.io/otel/metric", "WithUnit").Call(jen.Lit("ms")),
			jen.Qual("go.opentelemetry.io/otel/metric", "WithDescription").Call(jen.Lit("Duration of MCP calls handled by the generated adapter in milliseconds.")),
		),
		jen.Return(jen.Id("callCounter"), jen.Id("errorCounter"), jen.Id("durationHistogram")),
	)
	stmt.Line()

	// startTelemetry
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("startTelemetry").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("method").String(),
			jen.Id("attrs").Op("...").Qual("go.opentelemetry.io/otel/attribute", "KeyValue"),
		).
		Params(
			jen.Qual("context", "Context"),
			jen.Qual("go.opentelemetry.io/otel/trace", "Span"),
			jen.Qual("time", "Time"),
			jen.Index().Qual("go.opentelemetry.io/otel/attribute", "KeyValue"),
		).
		Block(
			jen.Id("baseAttrs").Op(":=").Append(jen.Index().Qual("go.opentelemetry.io/otel/attribute", "KeyValue").Values(
				jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("rpc.system"), jen.Lit("mcp")),
				jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("rpc.method"), jen.Id("method")),
				jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("mcp.service"), jen.Lit(data.ServiceName)),
			), jen.Id("attrs").Op("...")),
			jen.Id("tracer").Op(":=").Id("a").Dot("tracer"),
			jen.If(jen.Id("tracer").Op("==").Nil()).Block(
				jen.Id("tracer").Op("=").Qual("go.opentelemetry.io/otel", "Tracer").Call(jen.Id("defaultMCPAdapterTelemetryName").Call(jen.Id("a").Dot("opts"))),
			),
			jen.List(jen.Id("ctx"), jen.Id("span")).Op(":=").Id("tracer").Dot("Start").Call(jen.Id("ctx"), jen.Lit("mcp.").Op("+").Id("method")),
			jen.Id("span").Dot("SetAttributes").Call(jen.Id("baseAttrs").Op("...")),
			jen.Return(jen.Id("ctx"), jen.Id("span"), jen.Qual("time", "Now").Call(), jen.Id("baseAttrs")),
		)
	stmt.Line()

	// finishTelemetry
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("finishTelemetry").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("span").Qual("go.opentelemetry.io/otel/trace", "Span"),
			jen.Id("start").Qual("time", "Time"),
			jen.Id("attrs").Index().Qual("go.opentelemetry.io/otel/attribute", "KeyValue"),
			jen.Id("err").Error(),
			jen.Id("toolErr").Bool(),
		).
		Block(
			jen.Id("duration").Op(":=").Qual("time", "Since").Call(jen.Id("start")),
			jen.Id("statusClass").Op(":=").Lit("ok"),
			jen.If(jen.Id("err").Op("!=").Nil().Op("||").Id("toolErr")).Block(
				jen.Id("statusClass").Op("=").Lit("error"),
			),
			jen.Id("metricAttrs").Op(":=").Append(jen.Index().Qual("go.opentelemetry.io/otel/attribute", "KeyValue").Values(), jen.Id("attrs").Op("...")),
			jen.Id("metricAttrs").Op("=").Append(jen.Id("metricAttrs"),
				jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("status_class"), jen.Id("statusClass")),
				jen.Qual("go.opentelemetry.io/otel/attribute", "Bool").Call(jen.Lit("mcp.tool_error"), jen.Id("toolErr")),
			),
			jen.If(jen.Id("a").Dot("callCounter").Op("!=").Nil()).Block(
				jen.Id("a").Dot("callCounter").Dot("Add").Call(jen.Id("ctx"), jen.Lit(1), jen.Qual("go.opentelemetry.io/otel/metric", "WithAttributes").Call(jen.Id("metricAttrs").Op("..."))),
			),
			jen.If(jen.Id("a").Dot("durationHistogram").Op("!=").Nil()).Block(
				jen.Id("a").Dot("durationHistogram").Dot("Record").Call(jen.Id("ctx"), jen.Float64().Call(jen.Id("duration").Dot("Microseconds").Call()).Op("/").Lit(1000.0), jen.Qual("go.opentelemetry.io/otel/metric", "WithAttributes").Call(jen.Id("metricAttrs").Op("..."))),
			),
			jen.If(jen.Parens(jen.Id("err").Op("!=").Nil().Op("||").Id("toolErr")).Op("&&").Id("a").Dot("errorCounter").Op("!=").Nil()).Block(
				jen.Id("a").Dot("errorCounter").Dot("Add").Call(jen.Id("ctx"), jen.Lit(1), jen.Qual("go.opentelemetry.io/otel/metric", "WithAttributes").Call(jen.Id("metricAttrs").Op("..."))),
			),
			jen.Id("span").Dot("SetAttributes").Call(
				jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("status_class"), jen.Id("statusClass")),
				jen.Qual("go.opentelemetry.io/otel/attribute", "Bool").Call(jen.Lit("mcp.tool_error"), jen.Id("toolErr")),
				jen.Qual("go.opentelemetry.io/otel/attribute", "Int64").Call(jen.Lit("mcp.duration_ms"), jen.Id("duration").Dot("Milliseconds").Call()),
			),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("span").Dot("RecordError").Call(jen.Id("err")),
				jen.Id("span").Dot("SetStatus").Call(jen.Qual("go.opentelemetry.io/otel/codes", "Error"), jen.Id("err").Dot("Error").Call()),
			).Else().If(jen.Id("toolErr")).Block(
				jen.Id("span").Dot("SetStatus").Call(jen.Qual("go.opentelemetry.io/otel/codes", "Error"), jen.Lit("tool returned MCP error result")),
			).Else().Block(
				jen.Id("span").Dot("SetStatus").Call(jen.Qual("go.opentelemetry.io/otel/codes", "Ok"), jen.Lit("")),
			),
			jen.Id("span").Dot("End").Call(),
		)
	stmt.Line()
}

// emitStringPtrAndBuildContentItem generates stringPtr, isLikelyJSON, and buildContentItem.
func emitStringPtrAndBuildContentItem(stmt *jen.Statement) {
	stmt.Func().Id("stringPtr").Params(jen.Id("s").String()).Op("*").String().
		Block(jen.Return(jen.Op("&").Id("s")))
	stmt.Line()

	stmt.Func().Id("isLikelyJSON").Params(jen.Id("s").String()).Bool().
		Block(jen.Return(jen.Qual("encoding/json", "Valid").Call(jen.Index().Byte().Call(jen.Id("s")))))
	stmt.Line()

	stmt.Comment("buildContentItem returns a ContentItem honoring StructuredStreamJSON option.").Line()
	stmt.Func().Id("buildContentItem").Params(jen.Id("a").Op("*").Id("MCPAdapter"), jen.Id("s").String()).Op("*").Id("ContentItem").
		Block(
			jen.If(jen.Id("a").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Op("!=").Nil().Op("&&").Id("a").Dot("opts").Dot("StructuredStreamJSON").Op("&&").Id("isLikelyJSON").Call(jen.Id("s"))).Block(
				jen.Id("mt").Op(":=").Id("stringPtr").Call(jen.Lit("application/json")),
				jen.Return(jen.Op("&").Id("ContentItem").Values(jen.Dict{
					jen.Id("Type"):     jen.Lit("text"),
					jen.Id("MimeType"): jen.Id("mt"),
					jen.Id("Text"):     jen.Op("&").Id("s"),
				})),
			),
			jen.Return(jen.Op("&").Id("ContentItem").Values(jen.Dict{
				jen.Id("Type"): jen.Lit("text"),
				jen.Id("Text"): jen.Op("&").Id("s"),
			})),
		)
	stmt.Line()
}

// emitSendToolError generates the sendToolError method.
func emitSendToolError(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("sendToolError").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("stream").Id("ToolsCallServerStream"),
			jen.Id("toolName").String(),
			jen.Id("err").Error(),
		).Error().
		Block(
			jen.If(jen.Id("err").Op("==").Nil()).Block(jen.Return(jen.Nil())),
			jen.Id("mapped").Op(":=").Id("a").Dot("mapError").Call(jen.Id("err")),
			jen.If(jen.Id("mapped").Op("==").Nil()).Block(
				jen.Id("mapped").Op("=").Id("err"),
			),
			jen.Id("isError").Op(":=").True(),
			jen.Id("result").Op(":=").Op("&").Id("ToolsCallResult").Values(jen.Dict{
				jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
					jen.Id("buildContentItem").Call(jen.Id("a"), jen.Id("formatToolErrorText").Call(jen.Id("mapped"))),
				),
				jen.Id("IsError"): jen.Op("&").Id("isError"),
			}),
			jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"):   jen.Lit("tools/call"),
				jen.Lit("name"):     jen.Id("toolName"),
				jen.Lit("is_error"): jen.True(),
			})),
			jen.Return(jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Id("result"))),
		)
	stmt.Line()
}

// emitFormatToolErrorText generates the formatToolErrorText function.
func emitFormatToolErrorText(stmt *jen.Statement) {
	stmt.Func().Id("formatToolErrorText").Params(jen.Id("err").Error()).String().
		Block(
			jen.If(jen.Id("err").Op("==").Nil()).Block(
				jen.Return(jen.Lit("[internal_error] Tool execution failed.")),
			),
			jen.Id("code").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("goa").Dot("ErrorRemedyCode").Call(jen.Id("err"))),
			jen.If(jen.Id("code").Op("==").Lit("")).Block(
				jen.Var().Id("namer").Id("goa").Dot("LoomErrorNamer"),
				jen.If(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("namer"))).Block(
					jen.Id("code").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("namer").Dot("LoomErrorName").Call()),
				),
			),
			jen.If(jen.Id("code").Op("==").Lit("")).Block(
				jen.If(jen.List(jen.Id("status"), jen.Id("ok")).Op(":=").Id("goa").Dot("ErrorStatusCode").Call(jen.Id("err")), jen.Id("ok")).Block(
					jen.Switch(jen.Id("status")).Block(
						jen.Case(jen.Qual("net/http", "StatusBadRequest")).Block(
							jen.Id("code").Op("=").Lit("invalid_params"),
						),
						jen.Case(jen.Qual("net/http", "StatusNotFound")).Block(
							jen.Id("code").Op("=").Lit("not_found"),
						),
						jen.Default().Block(
							jen.Id("code").Op("=").Lit("internal_error"),
						),
					),
				),
			),
			jen.If(jen.Id("code").Op("==").Lit("")).Block(
				jen.Id("code").Op("=").Lit("internal_error"),
			),
			jen.Id("message").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("goa").Dot("ErrorSafeMessage").Call(jen.Id("err"))),
			jen.If(jen.Id("message").Op("==").Lit("")).Block(
				jen.Id("message").Op("=").Lit("Tool execution failed."),
			),
			jen.Id("recovery").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("goa").Dot("ErrorRetryHint").Call(jen.Id("err"))),
			jen.If(jen.Id("recovery").Op("==").Lit("")).Block(
				jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("[%s] %s"), jen.Id("code"), jen.Id("message"))),
			),
			jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("[%s] %s\nRecovery: %s"), jen.Id("code"), jen.Id("message"), jen.Id("recovery"))),
		)
	stmt.Line()
}

// emitToolCallError generates the toolCallError function.
func emitToolCallError(stmt *jen.Statement) {
	stmt.Func().Id("toolCallError").Params(jen.Id("err").Error(), jen.Id("defaultCode").String(), jen.Id("defaultRecovery").String()).Error().
		Block(
			jen.If(jen.Id("err").Op("==").Nil()).Block(
				jen.Id("err").Op("=").Id("goa").Dot("PermanentError").Call(jen.Id("defaultCode"), jen.Lit("Tool execution failed.")),
			),
			jen.Id("code").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("goa").Dot("ErrorRemedyCode").Call(jen.Id("err"))),
			jen.If(jen.Id("code").Op("==").Lit("")).Block(
				jen.Id("code").Op("=").Id("defaultCode"),
			),
			jen.Id("message").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("goa").Dot("ErrorSafeMessage").Call(jen.Id("err"))),
			jen.If(jen.Id("message").Op("==").Lit("")).Block(
				jen.Id("message").Op("=").Lit("Tool execution failed."),
			),
			jen.Id("recovery").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("goa").Dot("ErrorRetryHint").Call(jen.Id("err"))),
			jen.If(jen.Id("recovery").Op("==").Lit("")).Block(
				jen.Id("recovery").Op("=").Id("defaultRecovery"),
			),
			jen.Return(jen.Id("goa").Dot("WithErrorRemedy").Call(
				jen.Id("goa").Dot("PermanentError").Call(jen.Id("code"), jen.Lit("%s"), jen.Id("message")),
				jen.Op("&").Id("goa").Dot("ErrorRemedy").Values(jen.Dict{
					jen.Id("Code"):        jen.Id("code"),
					jen.Id("SafeMessage"): jen.Id("message"),
					jen.Id("RetryHint"):   jen.Id("recovery"),
				}),
			)),
		)
	stmt.Line()
}

// emitToolInputError generates toolInputError.
func emitToolInputError(stmt *jen.Statement) {
	stmt.Func().Id("toolInputError").Params(jen.Id("err").Error(), jen.Id("raw").Qual("encoding/json", "RawMessage")).Error().
		Block(
			jen.Return(jen.Id("toolCallError").Call(jen.Id("err"), jen.Lit("invalid_params"), jen.Id("inferToolInputRecovery").Call(jen.Id("err"), jen.Id("raw")))),
		)
	stmt.Line()
}

// emitInferToolInputRecovery generates inferToolInputRecovery.
func emitInferToolInputRecovery(stmt *jen.Statement) {
	stmt.Func().Id("inferToolInputRecovery").Params(jen.Id("err").Error(), jen.Id("raw").Qual("encoding/json", "RawMessage")).String().
		Block(
			jen.Id("message").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("goa").Dot("ErrorSafeMessage").Call(jen.Id("err"))),
			jen.If(jen.Id("message").Op("==").Lit("")).Block(
				jen.Id("message").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("err").Dot("Error").Call()),
			),
			jen.If(jen.Id("field").Op(":=").Id("missingFieldFromMessage").Call(jen.Id("message")), jen.Id("field").Op("!=").Lit("")).Block(
				jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("Include required field %q."), jen.Id("field"))),
			),
			jen.If(jen.List(jen.Id("action"), jen.Id("ok")).Op(":=").Id("actionValueEnvelopeExample").Call(jen.Id("raw")), jen.Id("ok")).Block(
				jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("Include the nested value object. Example: %s"), jen.Id("action"))),
			),
			jen.If(jen.Qual("strings", "Contains").Call(jen.Id("message"), jen.Lit("unexpected end of JSON input")).Op("||").Qual("strings", "Contains").Call(jen.Id("message"), jen.Lit("unexpected EOF"))).Block(
				jen.Return(jen.Lit("Provide complete JSON arguments. If a field expects an object, include {} instead of leaving it incomplete.")),
			),
			jen.Return(jen.Lit("Provide valid tool arguments.")),
		)
	stmt.Line()
}

// emitMissingFieldFromMessage generates missingFieldFromMessage.
func emitMissingFieldFromMessage(stmt *jen.Statement) {
	stmt.Func().Id("missingFieldFromMessage").Params(jen.Id("message").String()).String().
		Block(
			jen.Const().Id("prefix").Op("=").Lit("Missing required field: "),
			jen.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("message"), jen.Id("prefix"))).Block(
				jen.Return(jen.Lit("")),
			),
			jen.Return(jen.Qual("strings", "TrimSpace").Call(jen.Qual("strings", "TrimPrefix").Call(jen.Id("message"), jen.Id("prefix")))),
		)
	stmt.Line()
}

// emitActionValueEnvelopeExample generates actionValueEnvelopeExample and actionValueExampleForObject.
func emitActionValueEnvelopeExample(stmt *jen.Statement) {
	stmt.Func().Id("actionValueEnvelopeExample").Params(jen.Id("raw").Qual("encoding/json", "RawMessage")).Params(jen.String(), jen.Bool()).
		Block(
			jen.Var().Id("fields").Map(jen.String()).Qual("encoding/json", "RawMessage"),
			jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("fields")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Lit(""), jen.False()),
			),
			jen.If(jen.List(jen.Id("action"), jen.Id("ok")).Op(":=").Id("actionValueExampleForObject").Call(jen.Id("fields")), jen.Id("ok")).Block(
				jen.Return(jen.Id("action"), jen.True()),
			),
			jen.For(jen.List(jen.Id("name"), jen.Id("nestedRaw")).Op(":=").Range().Id("fields")).Block(
				jen.Var().Id("nested").Map(jen.String()).Qual("encoding/json", "RawMessage"),
				jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("nestedRaw"), jen.Op("&").Id("nested")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Continue(),
				),
				jen.If(jen.List(jen.Id("example"), jen.Id("ok")).Op(":=").Id("actionValueExampleForObject").Call(jen.Id("nested")), jen.Id("ok")).Block(
					jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit(`{"%s":%s}`), jen.Id("name"), jen.Id("example")), jen.True()),
				),
			),
			jen.Return(jen.Lit(""), jen.False()),
		)
	stmt.Line()

	stmt.Func().Id("actionValueExampleForObject").Params(jen.Id("fields").Map(jen.String()).Qual("encoding/json", "RawMessage")).Params(jen.String(), jen.Bool()).
		Block(
			jen.List(jen.Id("actionRaw"), jen.Id("hasAction")).Op(":=").Id("fields").Index(jen.Lit("action")),
			jen.If(jen.Op("!").Id("hasAction")).Block(
				jen.Return(jen.Lit(""), jen.False()),
			),
			jen.If(jen.List(jen.Id("_"), jen.Id("hasValue")).Op(":=").Id("fields").Index(jen.Lit("value")), jen.Id("hasValue")).Block(
				jen.Return(jen.Lit(""), jen.False()),
			),
			jen.Var().Id("action").String(),
			jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("actionRaw"), jen.Op("&").Id("action")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Lit(""), jen.False()),
			),
			jen.Id("action").Op("=").Qual("strings", "TrimSpace").Call(jen.Id("action")),
			jen.If(jen.Id("action").Op("==").Lit("")).Block(
				jen.Return(jen.Lit(""), jen.False()),
			),
			jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit(`{"action":%q,"value":{}}`), jen.Id("action")), jen.True()),
		)
	stmt.Line()
}

// emitFormatToolSuccessText generates formatToolSuccessText.
func emitFormatToolSuccessText(stmt *jen.Statement) {
	stmt.Func().Id("formatToolSuccessText").Params(jen.Id("v").Any()).String().
		Block(
			jen.Switch(jen.Id("value").Op(":=").Id("v").Assert(jen.Type())).Block(
				jen.Case(jen.Nil()).Block(jen.Return(jen.Lit("OK"))),
				jen.Case(jen.String()).Block(
					jen.If(jen.Qual("strings", "TrimSpace").Call(jen.Id("value")).Op("==").Lit("")).Block(
						jen.Return(jen.Lit("OK")),
					),
					jen.Return(jen.Id("value")),
				),
				jen.Case(jen.Op("*").String()).Block(
					jen.If(jen.Id("value").Op("==").Nil().Op("||").Qual("strings", "TrimSpace").Call(jen.Op("*").Id("value")).Op("==").Lit("")).Block(
						jen.Return(jen.Lit("OK")),
					),
					jen.Return(jen.Op("*").Id("value")),
				),
				jen.Case(jen.Bool()).Block(
					jen.If(jen.Id("value")).Block(jen.Return(jen.Lit("true"))),
					jen.Return(jen.Lit("false")),
				),
				jen.Case(jen.Op("*").Bool()).Block(
					jen.If(jen.Id("value").Op("==").Nil()).Block(jen.Return(jen.Lit("OK"))),
					jen.If(jen.Op("*").Id("value")).Block(jen.Return(jen.Lit("true"))),
					jen.Return(jen.Lit("false")),
				),
			),
			jen.List(jen.Id("normalized"), jen.Id("ok")).Op(":=").Id("normalizeToolSuccessValue").Call(jen.Id("v")),
			jen.If(jen.Op("!").Id("ok")).Block(
				jen.Return(jen.Qual("fmt", "Sprint").Call(jen.Id("v"))),
			),
			jen.Return(jen.Id("summarizeToolSuccessValue").Call(jen.Id("normalized"))),
		)
	stmt.Line()
}

// emitNormalizeToolSuccessValue generates normalizeToolSuccessValue.
func emitNormalizeToolSuccessValue(stmt *jen.Statement) {
	stmt.Func().Id("normalizeToolSuccessValue").Params(jen.Id("v").Any()).Params(jen.Any(), jen.Bool()).
		Block(
			jen.If(jen.Id("v").Op("==").Nil()).Block(jen.Return(jen.Nil(), jen.False())),
			jen.List(jen.Id("raw"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("v")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.False())),
			jen.Var().Id("normalized").Any(),
			jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("normalized")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.False()),
			),
			jen.Return(jen.Id("normalized"), jen.True()),
		)
	stmt.Line()
}

// emitSummarizeToolSuccessValue generates summarizeToolSuccessValue.
func emitSummarizeToolSuccessValue(stmt *jen.Statement) {
	stmt.Func().Id("summarizeToolSuccessValue").Params(jen.Id("v").Any()).String().
		Block(
			jen.Switch(jen.Id("value").Op(":=").Id("v").Assert(jen.Type())).Block(
				jen.Case(jen.Nil()).Block(jen.Return(jen.Lit("OK"))),
				jen.Case(jen.String()).Block(
					jen.If(jen.Qual("strings", "TrimSpace").Call(jen.Id("value")).Op("==").Lit("")).Block(jen.Return(jen.Lit("OK"))),
					jen.Return(jen.Id("value")),
				),
				jen.Case(jen.Bool()).Block(
					jen.If(jen.Id("value")).Block(jen.Return(jen.Lit("true"))),
					jen.Return(jen.Lit("false")),
				),
				jen.Case(jen.Float64()).Block(
					jen.Return(jen.Qual("strconv", "FormatFloat").Call(jen.Id("value"), jen.LitRune('f'), jen.Lit(-1), jen.Lit(64))),
				),
				jen.Case(jen.Index().Any()).Block(
					jen.Return(jen.Id("summarizeToolSuccessList").Call(jen.Id("value"))),
				),
				jen.Case(jen.Map(jen.String()).Any()).Block(
					jen.Return(jen.Id("summarizeToolSuccessMap").Call(jen.Id("value"))),
				),
				jen.Default().Block(
					jen.Return(jen.Qual("fmt", "Sprint").Call(jen.Id("value"))),
				),
			),
		)
	stmt.Line()
}

// emitSummarizeToolSuccessList generates summarizeToolSuccessList.
func emitSummarizeToolSuccessList(stmt *jen.Statement) {
	stmt.Func().Id("summarizeToolSuccessList").Params(jen.Id("items").Index().Any()).String().
		Block(
			jen.If(jen.Len(jen.Id("items")).Op("==").Lit(0)).Block(
				jen.Return(jen.Lit("No items.")),
			),
			jen.Id("parts").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Id("min").Call(jen.Len(jen.Id("items")), jen.Lit(5))),
			jen.For(jen.List(jen.Id("_"), jen.Id("item")).Op(":=").Range().Id("items")).Block(
				jen.Id("part").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("summarizeToolSuccessValue").Call(jen.Id("item"))),
				jen.If(jen.Id("part").Op("==").Lit("")).Block(jen.Continue()),
				jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.Id("part")),
				jen.If(jen.Len(jen.Id("parts")).Op("==").Lit(5)).Block(jen.Break()),
			),
			jen.If(jen.Len(jen.Id("parts")).Op("==").Lit(0)).Block(
				jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("%d items."), jen.Len(jen.Id("items")))),
			),
			jen.If(jen.Len(jen.Id("items")).Op(">").Len(jen.Id("parts"))).Block(
				jen.Id("parts").Op("=").Append(jen.Id("parts"), jen.Qual("fmt", "Sprintf").Call(jen.Lit("... (%d total)"), jen.Len(jen.Id("items")))),
			),
			jen.Return(jen.Qual("strings", "Join").Call(jen.Id("parts"), jen.Lit("\n"))),
		)
	stmt.Line()
}

// emitSummarizeToolSuccessMap generates summarizeToolSuccessMap.
func emitSummarizeToolSuccessMap(stmt *jen.Statement) {
	stmt.Func().Id("summarizeToolSuccessMap").Params(jen.Id("fields").Map(jen.String()).Any()).String().
		Block(
			jen.Id("preferredScalars").Op(":=").Index().String().Values(
				jen.Lit("result"), jen.Lit("output"), jen.Lit("summary"), jen.Lit("message"),
				jen.Lit("value"), jen.Lit("name"), jen.Lit("ack"), jen.Lit("sentiment"), jen.Lit("status"),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("key")).Op(":=").Range().Id("preferredScalars")).Block(
				jen.If(jen.List(jen.Id("scalar"), jen.Id("ok")).Op(":=").Id("scalarToolSuccessText").Call(jen.Id("fields").Index(jen.Id("key"))), jen.Id("ok")).Block(
					jen.Return(jen.Id("scalar")),
				),
			),
			jen.Id("preferredLists").Op(":=").Index().String().Values(
				jen.Lit("items"), jen.Lit("results"), jen.Lit("keywords"), jen.Lit("templates"), jen.Lit("documents"),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("key")).Op(":=").Range().Id("preferredLists")).Block(
				jen.If(jen.List(jen.Id("list"), jen.Id("ok")).Op(":=").Id("fields").Index(jen.Id("key")).Assert(jen.Index().Any()), jen.Id("ok")).Block(
					jen.Return(jen.Id("summarizeToolSuccessList").Call(jen.Id("list"))),
				),
			),
			jen.If(jen.Len(jen.Id("fields")).Op("==").Lit(1)).Block(
				jen.For(jen.List(jen.Id("_"), jen.Id("value")).Op(":=").Range().Id("fields")).Block(
					jen.Return(jen.Id("summarizeToolSuccessValue").Call(jen.Id("value"))),
				),
			),
			jen.If(jen.List(jen.Id("name"), jen.Id("ok")).Op(":=").Id("scalarToolSuccessText").Call(jen.Id("fields").Index(jen.Lit("name"))), jen.Id("ok")).Block(
				jen.If(jen.List(jen.Id("version"), jen.Id("ok")).Op(":=").Id("scalarToolSuccessText").Call(jen.Id("fields").Index(jen.Lit("version"))), jen.Id("ok")).Block(
					jen.Return(jen.Qual("strings", "TrimSpace").Call(jen.Id("name").Op("+").Lit(" ").Op("+").Id("version"))),
				),
			),
			jen.Id("keys").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("fields"))),
			jen.For(jen.Id("key").Op(":=").Range().Id("fields")).Block(
				jen.Id("keys").Op("=").Append(jen.Id("keys"), jen.Id("key")),
			),
			jen.Qual("sort", "Strings").Call(jen.Id("keys")),
			jen.If(jen.Len(jen.Id("keys")).Op(">").Lit(4)).Block(
				jen.Id("keys").Op("=").Id("keys").Index(jen.Empty(), jen.Lit(4)),
			),
			jen.Return(jen.Qual("fmt", "Sprintf").Call(jen.Lit("Fields: %s"), jen.Qual("strings", "Join").Call(jen.Id("keys"), jen.Lit(", ")))),
		)
	stmt.Line()
}

// emitScalarToolSuccessText generates scalarToolSuccessText.
func emitScalarToolSuccessText(stmt *jen.Statement) {
	stmt.Func().Id("scalarToolSuccessText").Params(jen.Id("v").Any()).Params(jen.String(), jen.Bool()).
		Block(
			jen.Switch(jen.Id("value").Op(":=").Id("v").Assert(jen.Type())).Block(
				jen.Case(jen.String()).Block(
					jen.Id("trimmed").Op(":=").Qual("strings", "TrimSpace").Call(jen.Id("value")),
					jen.Return(jen.Id("trimmed"), jen.Id("trimmed").Op("!=").Lit("")),
				),
				jen.Case(jen.Bool()).Block(
					jen.If(jen.Id("value")).Block(jen.Return(jen.Lit("true"), jen.True())),
					jen.Return(jen.Lit("false"), jen.True()),
				),
				jen.Case(jen.Float64()).Block(
					jen.Return(jen.Qual("strconv", "FormatFloat").Call(jen.Id("value"), jen.LitRune('f'), jen.Lit(-1), jen.Lit(64)), jen.True()),
				),
				jen.Default().Block(
					jen.Return(jen.Lit(""), jen.False()),
				),
			),
		)
	stmt.Line()
}

// emitInitializeHandler generates the Initialize method.
func emitInitializeHandler(stmt *jen.Statement, data *AdapterData) {
	stmt.Comment("Initialize handles the MCP initialize request.").Line()
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("Initialize").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("InitializePayload"),
		).
		Params(jen.Id("res").Op("*").Id("InitializeResult"), jen.Id("err").Error()).
		BlockFunc(func(g *jen.Group) {
			g.List(jen.Id("ctx"), jen.Id("span"), jen.Id("start"), jen.Id("attrs")).Op(":=").Id("a").Dot("startTelemetry").Call(jen.Id("ctx"), jen.Lit("initialize"))
			g.Defer().Func().Params().Block(
				jen.Id("a").Dot("finishTelemetry").Call(jen.Id("ctx"), jen.Id("span"), jen.Id("start"), jen.Id("attrs"), jen.Id("err"), jen.False()),
			).Call()

			g.Id("requestProtocol").Op(":=").Lit("")
			g.Id("requestSessionID").Op(":=").Id("mcpruntime").Dot("SessionIDFromContext").Call(jen.Id("ctx"))
			g.If(jen.Id("p").Op("!=").Nil()).Block(
				jen.Id("requestProtocol").Op("=").Id("p").Dot("ProtocolVersion"),
			)
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"):           jen.Lit("initialize"),
				jen.Lit("session_id"):       jen.Id("requestSessionID"),
				jen.Lit("protocol_version"): jen.Id("requestProtocol"),
			}))

			g.If(jen.Id("p").Op("==").Nil().Op("||").Id("p").Dot("ProtocolVersion").Op("==").Lit("")).Block(
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing protocolVersion"))),
			)
			g.If(jen.Op("!").Id("a").Dot("supportsProtocolVersion").Call(jen.Id("p").Dot("ProtocolVersion"))).Block(
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Unsupported protocol version"))),
			)

			g.Id("sessionID").Op(":=").Id("requestSessionID")
			g.If(jen.Id("sessionID").Op("==").Lit("").Op("&&").Id("mcpruntime").Dot("ResponseWriterFromContext").Call(jen.Id("ctx")).Op("!=").Nil()).Block(
				jen.Id("sessionID").Op("=").Id("mcpruntime").Dot("EnsureSessionID").Call(jen.Id("ctx")),
			)

			// Lock and check initialization
			g.Id("a").Dot("mu").Dot("Lock").Call()
			alreadyInitBlock := []jen.Code{
				jen.Id("a").Dot("mu").Dot("Unlock").Call(),
				jen.Id("err").Op("=").Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Already initialized")),
				jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("method"):           jen.Lit("initialize"),
					jen.Lit("session_id"):       jen.Id("sessionID"),
					jen.Lit("protocol_version"): jen.Id("p").Dot("ProtocolVersion"),
					jen.Lit("error"):            jen.Id("err").Dot("Error").Call(),
				})),
				jen.Return(jen.Nil(), jen.Id("err")),
			}
			g.If(jen.Id("sessionID").Op("==").Lit("")).Block(
				jen.If(jen.Id("a").Dot("initialized")).Block(alreadyInitBlock...),
				jen.Id("a").Dot("initialized").Op("=").True(),
			).Else().Block(
				jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("a").Dot("initializedSessions").Index(jen.Id("sessionID")), jen.Id("ok")).Block(alreadyInitBlock...),
				jen.Id("a").Dot("initializedSessions").Index(jen.Id("sessionID")).Op("=").Struct().Values(),
			)
			g.Id("a").Dot("mu").Dot("Unlock").Call()
			g.Id("a").Dot("captureSessionPrincipal").Call(jen.Id("ctx"), jen.Id("sessionID"))

			// ServerInfo
			g.Id("serverInfo").Op(":=").Op("&").Id("ServerInfo").ValuesFunc(func(v *jen.Group) {
				v.Id("Name").Op(":").Lit(data.MCPName)
				v.Id("Version").Op(":").Lit(data.MCPVersion)
				if data.WebsiteURL != "" {
					v.Id("WebsiteURL").Op(":").Id("stringPtr").Call(jen.Lit(data.WebsiteURL))
				}
				if len(data.Icons) > 0 {
					v.Id("Icons").Op(":").Add(iconSliceValue(data.Icons))
				}
			})

			// Capabilities
			g.Id("capabilities").Op(":=").Op("&").Id("ServerCapabilities").Values()
			if len(data.Tools) > 0 {
				g.Id("capabilities").Dot("Tools").Op("=").Op("&").Id("ToolsCapability").Values()
			}
			if len(data.Resources) > 0 {
				g.Id("capabilities").Dot("Resources").Op("=").Op("&").Id("ResourcesCapability").Values()
			}
			if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
				g.Id("capabilities").Dot("Prompts").Op("=").Op("&").Id("PromptsCapability").Values()
			}

			g.Id("res").Op("=").Op("&").Id("InitializeResult").Values(jen.Dict{
				jen.Id("ProtocolVersion"): jen.Id("a").Dot("mcpProtocolVersion").Call(),
				jen.Id("ServerInfo"):      jen.Id("serverInfo"),
				jen.Id("Capabilities"):    jen.Id("capabilities"),
			})
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"):           jen.Lit("initialize"),
				jen.Lit("session_id"):       jen.Id("sessionID"),
				jen.Lit("protocol_version"): jen.Id("res").Dot("ProtocolVersion"),
				jen.Lit("server_name"):      jen.Id("serverInfo").Dot("Name"),
			}))
			g.Return(jen.Id("res"), jen.Nil())
		})
	stmt.Line()
}

// emitPingHandler generates the Ping method.
func emitPingHandler(stmt *jen.Statement) {
	stmt.Comment("Ping handles the MCP ping request.").Line()
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("Ping").
		Params(jen.Id("ctx").Qual("context", "Context")).
		Params(jen.Id("res").Op("*").Id("PingResult"), jen.Id("err").Error()).
		Block(
			jen.List(jen.Id("ctx"), jen.Id("span"), jen.Id("start"), jen.Id("attrs")).Op(":=").Id("a").Dot("startTelemetry").Call(jen.Id("ctx"), jen.Lit("ping")),
			jen.Defer().Func().Params().Block(
				jen.Id("a").Dot("finishTelemetry").Call(jen.Id("ctx"), jen.Id("span"), jen.Id("start"), jen.Id("attrs"), jen.Id("err"), jen.False()),
			).Call(),
			jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("ping"),
			})),
			jen.Id("res").Op("=").Op("&").Id("PingResult").Values(jen.Dict{
				jen.Id("Pong"): jen.True(),
			}),
			jen.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("ping"),
			})),
			jen.Return(jen.Id("res"), jen.Nil()),
		)
	stmt.Line()
}
