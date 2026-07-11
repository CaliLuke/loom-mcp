package codegen

import (
	"bytes"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/template"

	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	generatorcodegen "github.com/CaliLuke/loom/codegen/generator"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/require"
)

func TestPrepareServices_RejectsUnmappedMCPMethods(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("calc", "add", "subtract")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "calc",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "add", Method: methods["add"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.Error(t, err)
	require.ErrorContains(t, err, `service "calc"`)
	require.ErrorContains(t, err, "subtract")
}

func TestGenerate_RejectsUnmappedPureMCPMethodsWithoutPrepareServices(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("calc", "add", "subtract")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "calc",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "add", Method: methods["add"]},
		},
	})

	_, err := Generate("example.com/calc/gen", []eval.Root{root}, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, `service "calc"`)
	require.ErrorContains(t, err, "subtract")
}

func TestGenerate_AcceptsMCPToolMethodsWithoutMethodLevelJSONRPC(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("demo", "ping")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "demo",
		Version: "0.1.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "ping", Method: methods["ping"]},
		},
	})

	_, err := Generate("example.com/demo/gen", []eval.Root{root}, nil)

	require.NoError(t, err)
}

func TestGenerateAdapter_NormalizesOmittedToolArguments(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("demo", "search")
	methods["search"].Payload = &expr.AttributeExpr{Type: &expr.Object{
		{Name: "query", Attribute: &expr.AttributeExpr{Type: expr.String}},
	}}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "demo",
		Version: "0.1.0",
		Tools:   []*mcpexpr.ToolExpr{{Name: "search", Method: methods["search"]}},
	})

	files, err := Generate("example.com/demo/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_demo", "adapter_server.go"))
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, `if len(bytes.TrimSpace(p.Arguments)) == 0 {
		normalized := *p
		normalized.Arguments = json.RawMessage([]byte("{}"))
		p = &normalized
	}`)
}

func TestMCPExprBuilder_EmitsServerCapabilitiesWithoutDuplicateCapabilitiesType(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "analyze")
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "analyze", Method: methods["analyze"]},
		},
	}
	builder := newMCPExprBuilder(svc, mcp, nil, 0)

	builder.buildMCPTypes()

	userTypes := builder.CollectUserTypes()
	typeNames := make([]string, 0, len(userTypes))
	for _, typ := range userTypes {
		typeNames = append(typeNames, typ.Name())
	}
	require.Contains(t, typeNames, "ServerCapabilities")
	require.NotContains(t, typeNames, "Capabilities")
}

func TestMCPExprBuilder_UsesNamespacedStreamingNotificationDefaults(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "analyze")
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "analyze", Method: methods["analyze"]},
		},
	}
	builder := newMCPExprBuilder(svc, mcp, &sourceSnapshot{
		jsonrpcRoutes: map[string]sourceJSONRPCRoute{
			"assistant": {method: http.MethodPost, path: "/rpc"},
		},
	}, 0)
	mcpService := builder.BuildServiceExpr()
	httpService := builder.buildHTTPService(mcpService)

	methodsByName := make(map[string]string)
	for _, endpoint := range httpService.HTTPEndpoints {
		if endpoint.SSE != nil {
			methodsByName[endpoint.MethodExpr.Name] = endpoint.SSE.NotificationMethod
		}
	}
	require.Equal(t, map[string]string{
		"events/stream": "",
		"tools/call":    "",
	}, methodsByName)
}

func TestPrepareServices_SynthesizesJSONRPCEndpointsForPureMCPMethods(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("demo", "ping")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "demo",
		Version: "0.1.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "ping", Method: methods["ping"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.NoError(t, err)

	jsonrpcSvc := root.API.JSONRPC.Service("demo")
	require.NotNil(t, jsonrpcSvc)
	require.Len(t, jsonrpcSvc.HTTPEndpoints, 1)
	require.Equal(t, "ping", jsonrpcSvc.HTTPEndpoints[0].MethodExpr.Name)
	require.NotNil(t, jsonrpcSvc.HTTPEndpoints[0].Meta["jsonrpc"])
	require.NotNil(t, methods["ping"].Meta["jsonrpc"])
	require.Len(t, jsonrpcSvc.HTTPEndpoints[0].Routes, 1)
	require.Equal(t, "/rpc", jsonrpcSvc.HTTPEndpoints[0].Routes[0].Path)
}

func TestPrepareServices_AllowsGenericTransportGenerationForPureMCPMethodsWithoutMethodLevelJSONRPC(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("demo", "ping")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "demo",
		Version: "0.1.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "ping", Method: methods["ping"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})
	require.NoError(t, err)
	jsonrpcSvc := root.API.JSONRPC.Service("demo")
	require.NotNil(t, jsonrpcSvc)
	require.Len(t, jsonrpcSvc.HTTPEndpoints, 1)
	require.NotEmpty(t, jsonrpcSvc.HTTPEndpoints[0].Responses)
	require.NotNil(t, jsonrpcSvc.HTTPEndpoints[0].Responses[0].Body)

	files, err := generatorcodegen.Transport("example.com/demo/gen", []eval.Root{root})

	require.NoError(t, err)
	require.NotEmpty(t, files)
}

func TestGenerateMCPClientAdapter_DoesNotRenderOriginalClientFallback(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("calc", "add")
	methods["add"].Result = &expr.AttributeExpr{Type: expr.Empty}
	mcp := &mcpexpr.MCPExpr{
		Name:    "calc",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "add", Method: methods["add"]},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/calc/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	files := generateMCPClientAdapter("example.com/calc/gen", svc, data)

	require.Len(t, files, 1)
	require.NotContains(t, renderGeneratedFile(t, files[0]), "origClient")
}

func TestGenerateMCPClientAdapter_RendersNotificationEndpoints(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "send_notification")
	methods["send_notification"].Payload = testNotificationPayload()
	methods["send_notification"].Result = &expr.AttributeExpr{Type: expr.Empty}
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Notifications: []*mcpexpr.NotificationExpr{
			{
				Name:   "status_update",
				Method: methods["send_notification"],
			},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	files := generateMCPClientAdapter("example.com/assistant/gen", svc, data)

	require.Len(t, files, 1)
	rendered := renderGeneratedFile(t, files[0])
	require.Contains(t, rendered, "e.SendNotification =")
	require.Contains(t, rendered, "NotifyStatusUpdate")
	require.NotContains(t, rendered, "NotifyNotifyStatusUpdate")
	require.Contains(t, rendered, "notificationPayload := &")
	require.Contains(t, rendered, "SendNotificationPayload{")
	require.Contains(t, rendered, "notificationPayload.Message =")
}

func TestGenerateMCPAdapter_RendersExperimentalEventsCapability(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "send_notification")
	methods["send_notification"].Payload = testNotificationPayload()
	methods["send_notification"].Result = &expr.AttributeExpr{Type: expr.Empty}
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Notifications: []*mcpexpr.NotificationExpr{
			{
				Name:   "status_update",
				Method: methods["send_notification"],
			},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()
	require.NoError(t, err)

	files := generateMCPTransport("example.com/assistant/gen", svc, data)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	rendered := renderGeneratedFile(t, adapterFile)

	require.Contains(t, rendered, `capabilities.Experimental = map[string]any{`)
	require.Contains(t, rendered, `"loom-mcp": map[string]any{`)
	require.Contains(t, rendered, `"events": map[string]any{`)
	require.Contains(t, rendered, `"method":        "events/stream"`)
	require.Contains(t, rendered, `"stream":        true`)
	require.Contains(t, rendered, `"notifications": []string{"notify_status_update"}`)
	require.Contains(t, rendered, `var _ Service = (*MCPAdapter)(nil)`)
	require.Contains(t, rendered, `func (a *MCPAdapter) NotifyStatusUpdate(ctx context.Context, p *SendNotificationPayload) error`)
	require.Contains(t, rendered, `n := &mcpruntime.Notification{`)
}

func TestGenerateMCPAdapter_RejectsNonEmptyListCursors(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "search", "read_document")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "search", Method: methods["search"]},
		},
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
		},
		Prompts: []*mcpexpr.PromptExpr{
			{
				Name: "code_review",
				Messages: []*mcpexpr.MessageExpr{
					{Role: "user", Content: "Review this code."},
				},
			},
		},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	rendered := renderGeneratedFile(t, adapterFile)

	require.Equal(t, 3, strings.Count(rendered, `if p != nil && p.Cursor != nil && *p.Cursor != "" {`))
	require.Equal(t, 3, strings.Count(rendered, `return nil, loom.PermanentError("invalid_params", "%s pagination is not implemented; cursor must be empty",`))
	require.Contains(t, rendered, `"tools/list")`)
	require.Contains(t, rendered, `"resources/list")`)
	require.Contains(t, rendered, `"prompts/list")`)
}

func TestGenerateMCPAdapter_DynamicPromptRequiredArgumentNameIsNotFormatString(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "generate_prompt")
	methods["generate_prompt"].Payload = &expr.AttributeExpr{Type: &expr.Object{
		{Name: "topic%s", Attribute: &expr.AttributeExpr{Type: expr.String}},
	}, Validation: &expr.ValidationExpr{Required: []string{"topic%s"}}}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
	})
	mcpexpr.Root.DynamicPrompts[svc.Name] = []*mcpexpr.DynamicPromptExpr{
		{Name: "assistant_prompt", Method: methods["generate_prompt"]},
	}

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	rendered := renderGeneratedFile(t, adapterFile)

	require.Contains(t, rendered, `loom.PermanentError("invalid_params", "Missing required argument: %s", "topic%s")`)
	require.NotContains(t, rendered, `loom.PermanentError("invalid_params", "Missing required argument: topic%s")`)
}

func TestGenerateMCPClientAdapter_RendersOriginalClientForResourceResults(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "read_document")
	methods["read_document"].Payload = testResourceQueryPayload()
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{
			{
				Name:   "documents",
				URI:    "doc://list",
				Method: methods["read_document"],
			},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	files := generateMCPClientAdapter("example.com/assistant/gen", svc, data)

	require.Len(t, files, 1)
	rendered := renderGeneratedFile(t, files[0])
	require.Contains(t, rendered, "origC :=")
	require.Contains(t, rendered, "origC.BuildReadDocumentRequest")
}

func TestGenerateMCPClientAdapter_RendersOriginalClientForDynamicPrompts(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "generate_prompt")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
	}
	mcpexpr.Root.RegisterMCP(svc, mcp)
	mcpexpr.Root.DynamicPrompts[svc.Name] = []*mcpexpr.DynamicPromptExpr{
		{Name: "assistant_prompt", Method: methods["generate_prompt"]},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, collectSourceSnapshot([]eval.Root{root}), 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	files := generateMCPClientAdapter("example.com/assistant/gen", svc, data)

	require.Len(t, files, 1)
	rendered := renderGeneratedFile(t, files[0])
	require.Contains(t, rendered, "origC :=")
	require.Contains(t, rendered, "origC.BuildGeneratePromptRequest")
}

func TestGenerateMCPClientAdapter_StaticPromptsOnlyDoesNotDeclareSessionDoer(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, _ := testService("assistant")
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Prompts: []*mcpexpr.PromptExpr{
			{
				Name: "summarize",
				Messages: []*mcpexpr.MessageExpr{
					{Role: "user", Content: "Summarize"},
				},
			},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	require.False(t, data.NeedsMCPClient)
	files := generateMCPClientAdapter("example.com/assistant/gen", svc, data)

	require.Len(t, files, 1)
	rendered := renderGeneratedFile(t, files[0])
	require.NotContains(t, rendered, "sessionDoer :=")
	require.NotContains(t, rendered, "mcpC :=")
	require.Contains(t, rendered, "e := &assistant.Endpoints{}")
}

func TestGeneratePromptProvider_RendersRuntimePromptRegistrar(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, _ := testService("assistant")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant",
		Version: "0.1.0",
		Prompts: []*mcpexpr.PromptExpr{
			{
				Name:        "greeting",
				Description: "Friendly greeting",
				Messages: []*mcpexpr.MessageExpr{
					{Role: "system", Content: "You are {{ .Name }}"},
				},
				Runtime: &mcpexpr.RuntimePromptExpr{
					AgentID: "assistant.chat",
					Role:    "system",
					Version: "v1",
				},
			},
		},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "prompt_provider.go"))
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, `func RegisterRuntimePrompts(reg *prompt.Registry) error {`)
	require.Contains(t, rendered, `ID:          prompt.Ident("greeting"),`)
	require.Contains(t, rendered, `AgentID:     "assistant.chat",`)
	require.Contains(t, rendered, `Role:        prompt.PromptRoleSystem,`)
	require.Contains(t, rendered, "Template:    \"You are {{ .Name }}\",")
	require.Contains(t, rendered, `Version:     "v1",`)
}

func TestGenerateAdapter_RendersSkillResourceProvider(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, _ := testService("assistant")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant",
		Version: "0.1.0",
		SkillDirectories: []*mcpexpr.SkillDirectoryExpr{
			{Root: ".agents/skills"},
		},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, `func skillSources() []mcpskills.Source {`)
	require.Contains(t, rendered, `Root: ".agents/skills"`)
	require.Contains(t, rendered, `if strings.HasPrefix(baseURI, "skill://") {`)
	require.Contains(t, rendered, `skillResources, err := mcpskills.List(ctx, skillSources)`)
	require.Contains(t, rendered, `skillNameToURI := make(map[string]string, len(skillResources))`)
	require.Contains(t, rendered, `if err := a.assertResourceURIAllowed(ctx, p.URI, skillNameToURI); err != nil {`)
	require.Contains(t, rendered, `content, err := mcpskills.Read(ctx, skillSources, baseURI)`)
	require.Contains(t, rendered, `a.log(ctx, "error", map[string]any{`)
	require.Contains(t, rendered, `"error":  err.Error(),`)
	require.Contains(t, rendered, `if errors.Is(err, mcpskills.ErrInvalidURI) {`)
	require.Contains(t, rendered, `} else if errors.Is(err, mcpskills.ErrNotFound) {`)
	require.Contains(t, rendered, `message := fmt.Sprintf("Unable to read skill resource: %s", baseURI)`)
	require.Contains(t, rendered, `return nil, a.safeMCPError(loom.PermanentError(code, "%s", message), code, message)`)
	require.NotContains(t, rendered, `loom.PermanentError("invalid_params", "%s", err.Error())`)
}

func TestGenerateAdapter_RendersSessionScopedEventPublishing(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "send_notification")
	methods["send_notification"].Payload = testNotificationPayload()
	methods["send_notification"].Result = &expr.AttributeExpr{Type: expr.Empty}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant",
		Version: "0.1.0",
		Notifications: []*mcpexpr.NotificationExpr{
			{Name: "status_update", Method: methods["send_notification"]},
		},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, `func (a *MCPAdapter) PublishSession(sessionID string, ev *EventsStreamResult)`)
	require.Contains(t, rendered, `if sessionID == "" {
		a.broadcaster.Publish(ev)
		return
	}`)
	require.Contains(t, rendered, `scoped.PublishSession(sessionID, ev)`)
	require.NotContains(t, rendered, `	}
	a.broadcaster.Publish(ev)
}

// PublishContext sends an event`)
	require.Contains(t, rendered, `func (a *MCPAdapter) PublishContext(ctx context.Context, ev *EventsStreamResult)`)
	require.Contains(t, rendered, `a.PublishContext(ctx, ev)`)
	require.Contains(t, rendered, `sessionID := mcpruntime.SessionIDFromContext(ctx)`)
	require.Contains(t, rendered, `sub, err = scoped.SubscribeSession(ctx, sessionID)`)
}

func TestGenerateSDKServer_MergesContextRequestHeadersIntoSyntheticRequest(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "system_info")
	methods["system_info"].Result = &expr.AttributeExpr{Type: expr.Empty}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "system_info", URI: "system://info", Method: methods["system_info"]},
		},
	}
	mcpexpr.Root.RegisterMCP(svc, mcp)

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	var rendered string
	for _, file := range files {
		if filepath.Base(file.Path) == "sdk_server.go" {
			rendered = renderGeneratedFile(t, file)
			break
		}
	}

	require.NotEmpty(t, rendered)
	require.Contains(t, rendered, "r = r.WithContext(mcpruntime.WithRequestHeaders(r.Context(), r.Header))")
	require.NotContains(t, rendered, "serveSDKEventsStream(server, adapter, w, r)")
	require.Contains(t, rendered, "if r.Method == http.MethodGet {")
	require.Contains(t, rendered, "adapter.assertSessionPrincipal(r.Context(), sessionID)")
	require.Contains(t, rendered, "http.Error(w, err.Error(), http.StatusForbidden)")
	require.Contains(t, rendered, "sdkSyntheticHTTPRequest(ctx, extra)")
	require.Contains(t, rendered, "for key, values := range mcpruntime.RequestHeadersFromContext(ctx)")

	// Header precedence: extra.Header must overlay ctx headers, not the
	// other way around. The per-JSON-RPC-call values that the SDK puts in
	// extra.Header should win over any stale values stored on ctx, which
	// is the runtime contract pinned by
	// TestRequestContextSeesPerCallHeaders in the assistant fixture and
	// the behavioral guarantee the autok review asked us to lock in here
	// at the codegen layer.
	ctxLoopIdx := strings.Index(rendered, "for key, values := range mcpruntime.RequestHeadersFromContext(ctx)")
	require.GreaterOrEqual(t, ctxLoopIdx, 0)
	extraLoopIdx := strings.Index(rendered, "for key, values := range extra.Header")
	require.GreaterOrEqual(t, extraLoopIdx, 0, "synthetic request must overlay extra.Header values")
	require.Greater(t, extraLoopIdx, ctxLoopIdx, "extra.Header overlay must come after the RequestHeadersFromContext copy so per-call headers win over the ctx-bridged values")
}

func TestGenerateSDKServer_SanitizesCollectedToolStreamErrors(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "system_info")
	methods["system_info"].Result = &expr.AttributeExpr{Type: expr.Empty}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "system_info", Method: methods["system_info"]},
		},
	}
	mcpexpr.Root.RegisterMCP(svc, mcp)

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	var rendered string
	for _, file := range files {
		if filepath.Base(file.Path) == "sdk_server.go" {
			rendered = renderGeneratedFile(t, file)
			break
		}
	}

	require.NotEmpty(t, rendered)
	require.Contains(t, rendered, `adapter *MCPAdapter`)
	require.Contains(t, rendered, `stream := &sdkToolCallCollector{adapter: a}`)
	require.Contains(t, rendered, `mapped = c.adapter.mapError(c.streamErr)`)
	require.Contains(t, rendered, `Text: stringPtr(formatToolErrorText(mapped))`)
	require.NotContains(t, rendered, `streamErr.Error()`)
}

func TestGenerateSDKServer_CompletionTotalCountsAllMatchesBeforeTruncation(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "generate_prompt")
	methods["generate_prompt"].Payload = &expr.AttributeExpr{Type: &expr.Object{
		{Name: "framework", Attribute: &expr.AttributeExpr{Type: expr.String, Validation: &expr.ValidationExpr{Values: []any{"react", "swiftui"}}}},
	}}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
	}
	mcpexpr.Root.RegisterMCP(svc, mcp)
	mcpexpr.Root.DynamicPrompts[svc.Name] = []*mcpexpr.DynamicPromptExpr{
		{Name: "assistant_prompt", Method: methods["generate_prompt"]},
	}

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	var rendered string
	for _, file := range files {
		if filepath.Base(file.Path) == "sdk_server.go" {
			rendered = renderGeneratedFile(t, file)
			break
		}
	}

	require.NotEmpty(t, rendered)
	totalIdx := strings.Index(rendered, "total := len(matches)")
	truncateIdx := strings.Index(rendered, "matches = matches[:100]")
	require.GreaterOrEqual(t, totalIdx, 0, "completion total must be captured before truncating returned values")
	require.GreaterOrEqual(t, truncateIdx, 0, "completion values must still be capped at the MCP limit")
	require.Less(t, totalIdx, truncateIdx, "completion total must count all matches, not only the truncated values")
	require.Contains(t, rendered, "return sdkCompleteValues(matches, total, hasMore)")
}

func TestGeneratedToolInfoIncludesMCPDiscoveryFields(t *testing.T) {
	files := generateToolDiscoveryFixture(t)
	serviceFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "service.go"))
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))

	serviceSource := renderGeneratedFile(t, serviceFile)
	require.Contains(t, serviceSource, "Title *string `json:\"title,omitempty\"")
	require.Contains(t, serviceSource, "OutputSchema any `json:\"outputSchema,omitempty\"")
	require.Contains(t, serviceSource, "Meta any `json:\"_meta,omitempty\"")

	adapterSource := renderGeneratedFile(t, adapterFile)
	require.Contains(t, adapterSource, "Title:")
	require.Contains(t, adapterSource, "stringPtr(\"Search Content\")")
	require.Contains(t, adapterSource, "OutputSchema: json.RawMessage([]byte(")
	require.Contains(t, adapterSource, "com.github.caliluke.loom-mcp/discovery")
	require.Contains(t, adapterSource, "category")
	require.Contains(t, adapterSource, "knowledge")
	require.Contains(t, adapterSource, "tags")
	require.Contains(t, adapterSource, "search")
	require.Contains(t, adapterSource, "retrieval")
	require.Contains(t, adapterSource, "keywords")
	require.Contains(t, adapterSource, "lookup")
	require.Contains(t, adapterSource, "documents")
}

func TestGeneratedToolInfoConversionsPreserveDiscoveryFields(t *testing.T) {
	files := generateToolDiscoveryFixture(t)
	// loom >= v1.3.0 splits large transport type files (types.go ->
	// types_*.go), so collect every types file in the package directory.
	serverSource := renderGeneratedTypesFiles(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "server"))
	clientSource := renderGeneratedTypesFiles(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "client"))

	for _, source := range []string{serverSource, clientSource} {
		require.Contains(t, source, "json:\"title,omitempty\"")
		require.Contains(t, source, "json:\"outputSchema,omitempty\"")
		require.Contains(t, source, "json:\"_meta,omitempty\"")
	}
	require.Contains(t, serverSource, "marshalMcpassistantToolInfoToToolInfoResponseBody")
	require.Contains(t, clientSource, "unmarshalToolInfoResponseBodyToMcpassistantToolInfo")
}

// renderGeneratedTypesFiles concatenates the rendered sources of every
// types*.go file generated in dir. loom < v1.3.0 emitted a single types.go;
// v1.3.0 split large transport type files into types_*.go.
func renderGeneratedTypesFiles(t *testing.T, files []*gcodegen.File, dir string) string {
	t.Helper()
	wantDir := filepath.ToSlash(dir)
	var sources []string
	for _, file := range files {
		path := filepath.ToSlash(file.Path)
		if filepath.ToSlash(filepath.Dir(path)) != wantDir {
			continue
		}
		base := filepath.Base(path)
		if base != "types.go" && !strings.HasPrefix(base, "types_") {
			continue
		}
		sources = append(sources, renderGeneratedFile(t, file))
	}
	require.NotEmptyf(t, sources, "no generated types files found in %s", wantDir)
	return strings.Join(sources, "\n")
}

func TestGeneratedSearchToolsHasOutputSchema(t *testing.T) {
	files := generateToolDiscoveryFixture(t)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	adapterSource := renderGeneratedFile(t, adapterFile)

	require.Contains(t, adapterSource, "func toolSearchToolInfo(name string) *ToolInfo")
	require.Contains(t, adapterSource, "OutputSchema: json.RawMessage([]byte(")
	require.Contains(t, adapterSource, `\"tools\"`)
	require.Contains(t, adapterSource, "total_matches")
	require.Contains(t, adapterSource, `\"truncated\"`)
}

func TestGeneratedSearchToolsRendersDesignSearchPolicy(t *testing.T) {
	files := generateToolDiscoveryFixture(t)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	adapterSource := renderGeneratedFile(t, adapterFile)

	require.Contains(t, adapterSource, "type ToolSearchWeights struct")
	require.Contains(t, adapterSource, "ExactMatchMode string")
	require.Contains(t, adapterSource, "FuzzyNameMatching *bool")
	require.Contains(t, adapterSource, "BroadFallback *bool")
}

func TestGeneratedMCPAdapterDropIfSlowDefaultsToDroppingSlowSubscribers(t *testing.T) {
	files := generateToolDiscoveryFixture(t)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	adapterSource := renderGeneratedFile(t, adapterFile)

	require.Contains(t, adapterSource, "DropIfSlow any")
	require.Contains(t, adapterSource, "drop := true")
	require.Contains(t, adapterSource, "drop = defaultMCPAdapterDropIfSlow(opts.DropIfSlow)")
	require.Contains(t, adapterSource, "case bool:")
	require.Contains(t, adapterSource, "case *bool:")
	require.NotContains(t, adapterSource, "if opts.DropIfSlow == false")
}

func TestGeneratedToolDiscoveryCallTemplateArguments(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "search_records")
	methods["search_records"].Payload = &expr.AttributeExpr{Type: &expr.Object{
		{Name: "query", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Search query"}},
		{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.Int, Description: "Maximum results"}},
	}}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{
				Name:                      "search_records",
				Description:               "Search records",
				DiscoveryCallTemplateArgs: map[string]any{"query": "login"},
				Method:                    methods["search_records"],
			},
		},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	adapterSource := renderGeneratedFile(t, adapterFile)

	require.Contains(t, adapterSource, "call_template_arguments")
	require.Contains(t, adapterSource, `\"query\":\"login\"`)
	require.Contains(t, adapterSource, "toolDiscoveryCallTemplateArguments")
	require.Contains(t, adapterSource, `for name, value := range toolDiscoveryCallTemplateArguments(tool)`)
}

func TestToolSearchDataFromExprAppliesDesignDefaults(t *testing.T) {
	data := toolSearchDataFromExpr(&mcpexpr.ToolSearchExpr{
		DefaultMaxResults: 3,
		MinScore:          50,
		ExactMatchMode:    mcpexpr.ToolSearchExactMatchBoost,
		FuzzyNameMatching: boolPtr(false),
		BroadFallback:     boolPtr(false),
		Weights: mcpexpr.ToolSearchWeightsExpr{
			Name:      intPtr(1200),
			FuzzyName: intPtr(700),
		},
	})

	require.Equal(t, 3, data.DefaultMaxResults)
	require.Equal(t, 50, data.MinScore)
	require.Equal(t, mcpexpr.ToolSearchExactMatchBoost, data.ExactMatchMode)
	require.False(t, data.FuzzyNameMatching)
	require.False(t, data.BroadFallback)
	require.Equal(t, 1200, data.NameWeight)
	require.Equal(t, defaultToolSearchTitleWeight, data.TitleWeight)
	require.Equal(t, 700, data.FuzzyNameWeight)
}

func TestGenerateSDKServerRendersToolSearchSyntheticTools(t *testing.T) {
	files := generateToolDiscoveryFixture(t)
	sdkFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "sdk_server.go"))
	sdkSource := renderGeneratedFile(t, sdkFile)

	require.Contains(t, sdkSource, "adapter.toolSearchEnabled()")
	require.Contains(t, sdkSource, "adapter.toolSearchSyntheticTools()")
	require.Contains(t, sdkSource, "adapter.visibleToolCatalog(adapter.generatedToolCatalog())")
	require.Contains(t, sdkSource, "SDK ToolSearch compact mode does not support AllowDirectHiddenCalls")
}

func TestBuildAdapterData_DefaultedEnumFieldsStayScalarAndReapplyDefaults(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "get_workflow")
	methods["get_workflow"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{
				Name: "workflow_id",
				Attribute: &expr.AttributeExpr{
					Type:         expr.String,
					DefaultValue: "prd-generation",
					Validation: &expr.ValidationExpr{
						Values: []any{"prd-generation", "technical-design"},
					},
				},
			},
		},
	}

	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "get_workflow", Method: methods["get_workflow"]},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	require.Len(t, data.Tools, 1)
	require.Equal(t, []EnumField{{Name: "workflow_id", Values: []string{"prd-generation", "technical-design"}, Pointer: false}}, data.Tools[0].EnumFields)
	require.Len(t, data.Tools[0].DefaultFields, 1)

	files := generateMCPTransport("example.com/assistant/gen", svc, data)
	var adapterFile *gcodegen.File
	for _, file := range files {
		if filepath.ToSlash(file.Path) == "gen/mcp_assistant/adapter_server.go" {
			adapterFile = file
			break
		}
	}
	require.NotNil(t, adapterFile)
	rendered := renderGeneratedFile(t, adapterFile)
	require.NotContains(t, rendered, "topLevelJSONFieldSet")
	require.Contains(t, rendered, "rawFields, err := decodeMCPPayloadFields(p.Arguments)")
	require.Contains(t, rendered, `if _, ok := rawFields["workflow_id"]; !ok {`)
	require.Contains(t, rendered, `payload.WorkflowID = "prd-generation"`)
	require.NotContains(t, rendered, "payload.WorkflowID != nil")
	require.NotContains(t, rendered, "*payload.WorkflowID")
}

func TestGenerateMCPTransport_EnumValidationTreatsOptionalPointerNullAsAbsent(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "get_workflow")
	methods["get_workflow"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{
				Name: "mode",
				Attribute: &expr.AttributeExpr{
					Type: expr.String,
					Validation: &expr.ValidationExpr{
						Values: []any{"draft", "final"},
					},
				},
			},
			{
				Name: "tone",
				Attribute: &expr.AttributeExpr{
					Type: expr.String,
					Validation: &expr.ValidationExpr{
						Values: []any{"brief", "detailed"},
					},
				},
			},
		},
		Validation: &expr.ValidationExpr{Required: []string{"tone"}},
	}
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "get_workflow", Method: methods["get_workflow"]},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	require.Len(t, data.Tools, 1)
	require.Equal(t, []EnumField{
		{Name: "mode", Values: []string{"draft", "final"}, Pointer: true},
		{Name: "tone", Values: []string{"brief", "detailed"}, Pointer: false},
	}, data.Tools[0].EnumFields)

	files := generateMCPTransport("example.com/assistant/gen", svc, data)
	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	rendered := renderGeneratedFile(t, adapterFile)

	require.Contains(t, rendered, "func validateMCPPayloadEnum(fields map[string]json.RawMessage, field string, optional bool, allowed ...string) error")
	require.Contains(t, rendered, `if optional && bytes.Equal(trimmed, []byte("null")) {`)
	require.Contains(t, rendered, `validateMCPPayloadEnum(rawFields, "mode", true, "draft", "final")`)
	require.Contains(t, rendered, `validateMCPPayloadEnum(rawFields, "tone", false, "brief", "detailed")`)
	require.Less(t,
		strings.Index(rendered, `validateMCPPayloadEnum(rawFields, "mode", true, "draft", "final")`),
		strings.Index(rendered, `validateMCPPayloadEnum(rawFields, "tone", false, "brief", "detailed")`),
	)
}

func TestGenerateMCPTransport_UnknownEntitiesUseMCPErrorNames(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "get_workflow", "read_document")
	methods["get_workflow"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "workflow_id", Attribute: &expr.AttributeExpr{Type: expr.String}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"workflow_id"}},
	}
	methods["read_document"].Payload = testResourceQueryPayload()
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "get_workflow", Method: methods["get_workflow"]},
		},
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
		},
		Prompts: []*mcpexpr.PromptExpr{
			{
				Name:        "summarize",
				Description: "Summarize a workflow",
				Messages: []*mcpexpr.MessageExpr{
					{Role: "user", Content: "Summarize"},
				},
			},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	files := generateMCPTransport("example.com/assistant/gen", svc, data)
	adapterFile := findGeneratedFile(t, files, "gen/mcp_assistant/adapter_server.go")
	rendered := renderGeneratedFile(t, adapterFile)

	require.Contains(t, rendered, `loom.PermanentError("invalid_params", "Unknown tool: %s"`)
	require.Contains(t, rendered, `loom.PermanentError("resource_not_found", "Unknown resource: %s"`)
	require.Contains(t, rendered, `loom.PermanentError("invalid_params", "Unknown prompt: %s"`)
	require.NotContains(t, rendered, `loom.PermanentError("method_not_found", "Unknown tool: %s"`)
	require.NotContains(t, rendered, `loom.PermanentError("method_not_found", "Unknown resource: %s"`)
	require.NotContains(t, rendered, `loom.PermanentError("method_not_found", "Unknown prompt: %s"`)
}

func TestGenerateAdapter_RendersSafeResourceAndPromptErrors(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "read_document")
	methods["read_document"].Payload = testResourceQueryPayload()
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
		},
		Prompts: []*mcpexpr.PromptExpr{
			{
				Name:        "summarize",
				Description: "Summarize a workflow",
				Messages: []*mcpexpr.MessageExpr{
					{Role: "user", Content: "Summarize"},
				},
			},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()
	require.NoError(t, err)

	files := generateMCPTransport("example.com/assistant/gen", svc, data)
	adapterFile := findGeneratedFile(t, files, "gen/mcp_assistant/adapter_server.go")
	rendered := renderGeneratedFile(t, adapterFile)

	require.Contains(t, rendered, `func (a *MCPAdapter) safeMCPError(err error, defaultCode string, fallbackMessage string) error {`)
	require.Contains(t, rendered, `remedy := loom.ExtractErrorRemedy(mapped)`)
	require.Contains(t, rendered, `return nil, a.safeMCPError(aerr, "invalid_params", "Invalid resource request.")`)
	require.Contains(t, rendered, `return nil, a.safeMCPError(err, "invalid_params", "Invalid resource request.")`)
	require.Contains(t, rendered, `return nil, a.safeMCPError(err, "internal_error", "Resource read failed.")`)
	require.Contains(t, rendered, `return nil, a.safeMCPError(serr, "internal_error", "Resource read failed.")`)
	require.Contains(t, rendered, `return nil, a.safeMCPError(err, "internal_error", "Prompt retrieval failed.")`)
	require.NotContains(t, rendered, `} else if err != nil {
				return nil, err
			}`)
	require.NotContains(t, rendered, `loom.PermanentError("invalid_params", "%s", err.Error())`)
}

func TestGenerateMCPClientAdapter_SpecializesResourceQueryConstruction(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "read_document")
	methods["read_document"].Payload = testResourceQueryPayload()
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{
			{
				Name:   "documents",
				URI:    "doc://list",
				Method: methods["read_document"],
			},
		},
	}
	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()

	require.NoError(t, err)
	files := generateMCPClientAdapter("example.com/assistant/gen", svc, data)

	require.Len(t, files, 1)
	rendered := renderGeneratedFile(t, files[0])
	require.NotContains(t, rendered, "map[string]any")
	require.NotContains(t, rendered, "sort.Strings")
	require.NotContains(t, rendered, "\"reflect\"")
	require.NotContains(t, rendered, "hasMCPQueryValue")
	require.NotContains(t, rendered, "encodeMCPQueryValue")
	require.Contains(t, rendered, "query := url.Values{}")
	require.Contains(t, rendered, "type sessionAwareDoer struct")
	require.Contains(t, rendered, "protocolVersion string")
	require.Contains(t, rendered, `if method != "initialize" && d.protocolVersion != "" {`)
	require.Contains(t, rendered, "req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, d.protocolVersion)")
	require.Contains(t, rendered, "protocolVersion: mcpAssistant.DefaultProtocolVersion")
	require.Contains(t, rendered, "func jsonRPCMethod(req *http.Request) (string, error)")
	require.Contains(t, rendered, `query.Add("cursor", payload.Cursor)`)
	require.Contains(t, rendered, "if payload.Offset != nil {")
	require.Contains(t, rendered, `query.Add("offset", strconv.FormatInt(int64(*payload.Offset), 10))`)
	require.Contains(t, rendered, "if payload.Limit != 0 {")
	require.Contains(t, rendered, `query.Add("limit", strconv.FormatUint(uint64(payload.Limit), 10))`)
	require.Contains(t, rendered, "if payload.Enabled != nil {")
	require.Contains(t, rendered, `query.Add("enabled", strconv.FormatBool(*payload.Enabled))`)
	require.Contains(t, rendered, "if payload.Ratio != nil {")
	require.Contains(t, rendered, `query.Add("ratio", strconv.FormatFloat(*payload.Ratio, 'g', -1, 64))`)
	require.Contains(t, rendered, "for _, value := range payload.Tags {")
	require.Contains(t, rendered, `query.Add("tags", value)`)
	require.Contains(t, rendered, `query.Add("tenant", payload.Tenant)`)
}

func TestBuildMCPProtocolVersionFileKeepsConfiguredDefault(t *testing.T) {
	file := buildMCPProtocolVersionFile("mcpdemo", "demo", "2025-06-18")
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, `const DefaultProtocolVersion = "2025-06-18"`)
	require.Contains(t, rendered, `"2025-11-25",`)
	require.Contains(t, rendered, `"2025-06-18",`)
}

func TestApplyMCPPolicyHeadersToJSONRPCMount_RewritesRawMountSection(t *testing.T) {
	header := gcodegen.Header("JSON-RPC server", "server", nil)
	file := &gcodegen.File{
		Path: "gen/jsonrpc/assistant/server/server.go",
		Sections: []gcodegen.Section{
			header,
			gcodegen.NewRawSection("jsonrpc-server-mount", `
// MountAssistant configures the mux to serve the JSON-RPC assistant service methods.
func MountAssistant(mux goahttp.Muxer, h *Server) {
	// Mixed transports: mount unified handler that negotiates HTTP vs SSE by Accept header and JSON-RPC method
	mux.Handle("POST", "/rpc", h.ServeHTTP)
}

// MountAssistant configures the mux to serve the JSON-RPC assistant service methods.
func (s *Server) MountAssistant(mux goahttp.Muxer) {
	MountAssistant(mux, s)
}
`),
		},
	}

	require.NoError(t, applyMCPPolicyHeadersToJSONRPCMount([]*gcodegen.File{file}, "2025-06-18"))

	rendered := renderGeneratedFile(t, file)
	require.Contains(t, rendered, `streamableHTTPSessions := mcpruntime.NewStreamableHTTPSessions()`)
	require.Contains(t, rendered, `requestCancellations := mcpruntime.NewRequestCancellationRegistry()`)
	require.Contains(t, rendered, `mux.Handle("POST", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, `mux.Handle("GET", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, `mux.Handle("DELETE", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, "func withMCPPolicyHeaders(streamableHTTPSessions *mcpruntime.StreamableHTTPSessions, requestCancellations *mcpruntime.RequestCancellationRegistry, next http.HandlerFunc) http.HandlerFunc {")
	require.Contains(t, rendered, "func validateMCPProtocolVersionHeader(r *http.Request) error {")
	require.Contains(t, rendered, `if method == "initialize" {`)
	require.Contains(t, rendered, `// 2025-03-26 compatibility version when no negotiated version is available.`)
	require.NotContains(t, rendered, `return fmt.Errorf("Missing %s header", mcpruntime.HeaderKeyProtocolVersion)`)
	require.Contains(t, rendered, `for _, supported := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"} {`)
	require.Contains(t, rendered, `return fmt.Errorf("Unsupported %s header %q", mcpruntime.HeaderKeyProtocolVersion, version)`)
	require.Contains(t, rendered, `ctx = mcpruntime.WithAllowedResourceNames(ctx, allow)`)
	require.Contains(t, rendered, `ctx = mcpruntime.WithDeniedResourceNames(ctx, deny)`)
	require.NotContains(t, rendered, `context.WithValue(ctx, "mcp_allow_names", allow)`)
	require.NotContains(t, rendered, `context.WithValue(ctx, "mcp_deny_names", deny)`)
	require.Contains(t, rendered, `ctx = mcpruntime.WithResponseWriter(ctx, w)`)
	require.Contains(t, rendered, `if acceptedMCPJSONRPCNotificationOrResponse(requestCancellations, r) {`)
	require.Contains(t, rendered, `w.WriteHeader(http.StatusAccepted)`)
	require.Contains(t, rendered, `requestCancellations.Cancel(r.Header.Get(mcpruntime.HeaderKeySessionID), requestID)`)
	require.Contains(t, rendered, `cleanup := requestCancellations.Register(sessionID, requestID, cancel)`)
	require.Contains(t, rendered, `if err := validateMCPStreamableHTTPSession(streamableHTTPSessions, r, method); err != nil {`)
	require.Contains(t, rendered, `if err := streamableHTTPSessions.Issue(issuedSessionID); err != nil {`)
	require.Contains(t, rendered, `cleanup, err := streamableHTTPSessions.RegisterListener(sessionID, cancel)`)
	require.Contains(t, rendered, `if err := sessions.Terminate(sessionID); err != nil {`)
	require.Contains(t, rendered, `http.Error(w, "Invalid or expired session ID", http.StatusNotFound)`)
	require.Contains(t, rendered, `case "notifications/cancelled":`)
	require.Contains(t, rendered, `case "notifications/initialized", "notifications/progress", "notifications/roots/list_changed":`)
}

func TestApplyMCPPolicyHeadersToJSONRPCMount_RewritesRawMountSectionBySourceShape(t *testing.T) {
	header := gcodegen.Header("JSON-RPC server", "server", nil)
	file := &gcodegen.File{
		Path: "gen/jsonrpc/assistant/server/server.go",
		Sections: []gcodegen.Section{
			header,
			gcodegen.NewRawSection("loom-jsonrpc-mount", `
// MountAssistant configures the mux to serve the JSON-RPC assistant service methods.
func MountAssistant(mux goahttp.Muxer, h *Server) {
	// Mixed transports: mount unified handler that negotiates HTTP vs SSE by Accept header and JSON-RPC method
	mux.Handle("POST", "/rpc", h.ServeHTTP)
	mux.Handle("GET", "/rpc", h.ServeHTTP)
}
`),
		},
	}

	require.NoError(t, applyMCPPolicyHeadersToJSONRPCMount([]*gcodegen.File{file}, "2025-06-18"))

	rendered := renderGeneratedFile(t, file)
	require.Contains(t, rendered, `mux.Handle("POST", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, `mux.Handle("GET", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, "func withMCPPolicyHeaders(streamableHTTPSessions *mcpruntime.StreamableHTTPSessions, requestCancellations *mcpruntime.RequestCancellationRegistry, next http.HandlerFunc) http.HandlerFunc {")
	require.Contains(t, rendered, `ctx = mcpruntime.WithAllowedResourceNames(ctx, allow)`)
	require.Contains(t, rendered, `ctx = mcpruntime.WithDeniedResourceNames(ctx, deny)`)
}

func TestApplyMCPPolicyHeadersToJSONRPCMount_RewritesJenniferMountSection(t *testing.T) {
	header := gcodegen.Header("JSON-RPC server", "server", nil)
	file := &gcodegen.File{
		Path: "gen/jsonrpc/assistant/server/server.go",
		Sections: []gcodegen.Section{
			header,
			gcodegen.NewJenniferSection("loom-jsonrpc-mount", func(stmt *jen.Statement) {
				stmt.Comment("MountAssistant configures the mux to serve the JSON-RPC assistant service methods.").Line()
				stmt.Func().Id("MountAssistant").
					Params(
						jen.Id("mux").Qual("github.com/CaliLuke/loom/http", "Muxer"),
						jen.Id("h").Op("*").Id("Server"),
					).
					Block(
						jen.Comment("Mixed transports: mount unified handler that negotiates HTTP vs SSE by Accept header and JSON-RPC method"),
						jen.Id("mux").Dot("Handle").Call(jen.Lit("POST"), jen.Lit("/rpc"), jen.Id("h").Dot("ServeHTTP")),
						jen.Id("mux").Dot("Handle").Call(jen.Lit("GET"), jen.Lit("/rpc"), jen.Id("h").Dot("ServeHTTP")),
					)
			}),
		},
	}

	require.NoError(t, applyMCPPolicyHeadersToJSONRPCMount([]*gcodegen.File{file}, "2025-06-18"))

	rendered := renderGeneratedFile(t, file)
	require.Contains(t, rendered, `mux.Handle("POST", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, `mux.Handle("GET", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, "func withMCPPolicyHeaders(streamableHTTPSessions *mcpruntime.StreamableHTTPSessions, requestCancellations *mcpruntime.RequestCancellationRegistry, next http.HandlerFunc) http.HandlerFunc {")
	require.Contains(t, rendered, `ctx = mcpruntime.WithAllowedResourceNames(ctx, allow)`)
	require.Contains(t, rendered, `ctx = mcpruntime.WithDeniedResourceNames(ctx, deny)`)
}

func TestApplyMCPPolicyHeadersToJSONRPCMount_FailsWhenMountShapeIsUnwrapped(t *testing.T) {
	header := gcodegen.Header("JSON-RPC server", "server", nil)
	file := &gcodegen.File{
		Path: "gen/jsonrpc/assistant/server/server.go",
		Sections: []gcodegen.Section{
			header,
			gcodegen.NewRawSection("jsonrpc-server-mount", `
// MountAssistant configures the mux to serve the JSON-RPC assistant service methods.
func MountAssistant(mux goahttp.Muxer, h *Server) {
	mux.Handle("POST", "/rpc", h.Serve)
}
`),
		},
	}

	err := applyMCPPolicyHeadersToJSONRPCMount([]*gcodegen.File{file}, "2025-06-18")

	require.Error(t, err)
	require.ErrorContains(t, err, "upstream JSON-RPC mount shape changed")
	require.ErrorContains(t, err, "jsonrpc-server-mount")
}

func TestApplyMCPJSONRPCErrorCodes_MapsResourceNotFound(t *testing.T) {
	header := gcodegen.Header("JSON-RPC server", "server", nil)
	file := &gcodegen.File{
		Path: "gen/jsonrpc/mcp_assistant/server/server.go",
		Sections: []gcodegen.Section{
			header,
			gcodegen.NewRawSection("server-handlers", `
switch en.LoomErrorName() {
case "invalid_params":
	encodeJSONRPCError(ctx, w, req, jsonrpc.InvalidParams, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)
case "method_not_found":
	encodeJSONRPCError(ctx, w, req, jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)
}
switch en.LoomErrorName() {
case "invalid_params":
	return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.InvalidParams, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))
case "method_not_found":
	return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))
}
`),
		},
	}

	err := applyMCPJSONRPCErrorCodes([]*gcodegen.File{file})

	require.NoError(t, err)
	rendered := renderGeneratedFile(t, file)
	require.Contains(t, rendered, `case "resource_not_found":`)
	require.Contains(t, rendered, `encodeJSONRPCError(ctx, w, req, jsonrpc.Code(-32002), loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)`)
	require.Contains(t, rendered, `return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.Code(-32002), loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))`)
}

func TestGenerate_ActualMCPServerMountIncludesPolicyWrapper(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "analyze_sentiment", "read_document")
	methods["read_document"].Payload = testResourceQueryPayload()
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		ToolSearch: &mcpexpr.ToolSearchExpr{
			DefaultMaxResults: 3,
			MinScore:          50,
			ExactMatchMode:    mcpexpr.ToolSearchExactMatchBoost,
			FuzzyNameMatching: boolPtr(false),
			BroadFallback:     boolPtr(false),
			Weights: mcpexpr.ToolSearchWeightsExpr{
				Name:      intPtr(1200),
				FuzzyName: intPtr(700),
			},
		},
		Tools: []*mcpexpr.ToolExpr{
			{Name: "analyze_sentiment", Method: methods["analyze_sentiment"]},
		},
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
		},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	var rendered string
	var adapterRendered string
	for _, file := range files {
		if file.Path == "gen/jsonrpc/mcp_assistant/server/server.go" {
			rendered = renderGeneratedFile(t, file)
		}
		if file.Path == "gen/mcp_assistant/adapter_server.go" {
			adapterRendered = renderGeneratedFile(t, file)
		}
	}

	require.NotEmpty(t, rendered)
	require.NotEmpty(t, adapterRendered)
	require.Contains(t, rendered, `mux.Handle("POST", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, `mux.Handle("GET", "/rpc", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))`)
	require.Contains(t, rendered, "func withMCPPolicyHeaders(streamableHTTPSessions *mcpruntime.StreamableHTTPSessions, requestCancellations *mcpruntime.RequestCancellationRegistry, next http.HandlerFunc) http.HandlerFunc {")
	require.Contains(t, rendered, `ctx = mcpruntime.WithAllowedResourceNames(ctx, allow)`)
	require.Contains(t, rendered, `ctx = mcpruntime.WithDeniedResourceNames(ctx, deny)`)
	require.NotContains(t, rendered, `context.WithValue(ctx, "mcp_allow_names", allow)`)
	require.NotContains(t, rendered, `context.WithValue(ctx, "mcp_deny_names", deny)`)
	require.Contains(t, rendered, `ctx = mcpruntime.WithResponseWriter(ctx, w)`)
	require.Contains(t, adapterRendered, `AllowedResourceNamesFromContext(ctx)`)
	require.Contains(t, adapterRendered, `DeniedResourceNamesFromContext(ctx)`)
	require.NotContains(t, adapterRendered, `ctx.Value("mcp_allow_names")`)
	require.NotContains(t, adapterRendered, `ctx.Value("mcp_deny_names")`)
}

func TestPrepareServices_RejectsNonPostJSONRPCPath(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "analyze")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcServiceWithMethod(svc, "/rpc", http.MethodGet),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "analyze", Method: methods["analyze"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.Error(t, err)
	require.ErrorContains(t, err, `service "assistant"`)
	require.ErrorContains(t, err, "JSONRPC")
	require.ErrorContains(t, err, "POST")
}

func TestPrepareServices_RejectsIncompatibleNotificationPayload(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "send_notification")
	methods["send_notification"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "status", Attribute: &expr.AttributeExpr{Type: expr.String}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"status"}},
	}
	methods["send_notification"].Result = &expr.AttributeExpr{Type: expr.Empty}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Notifications: []*mcpexpr.NotificationExpr{
			{Name: "status_update", Method: methods["send_notification"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.Error(t, err)
	require.ErrorContains(t, err, "send_notification")
	require.ErrorContains(t, err, "notification payload")
}

func TestPrepareServices_RejectsResultBearingNotificationMethod(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "send_notification")
	methods["send_notification"].Payload = testNotificationPayload()
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Notifications: []*mcpexpr.NotificationExpr{
			{Name: "status_update", Method: methods["send_notification"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.Error(t, err)
	require.ErrorContains(t, err, "send_notification")
	require.ErrorContains(t, err, "must not declare a result")
}

func TestPrepareServices_RejectsUnsupportedResourceQueryFieldType(t *testing.T) {
	testCases := []struct {
		name      string
		fieldName string
		fieldType expr.DataType
	}{
		{
			name:      "map",
			fieldName: "filters",
			fieldType: &expr.Map{
				KeyType:  &expr.AttributeExpr{Type: expr.String},
				ElemType: &expr.AttributeExpr{Type: expr.String},
			},
		},
		{
			name:      "array any",
			fieldName: "nums",
			fieldType: &expr.Array{ElemType: &expr.AttributeExpr{Type: expr.Any}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			restore := resetMCPCodegenState(t)
			defer restore()

			svc, methods := testService("assistant", "read_document")
			methods["read_document"].Payload = &expr.AttributeExpr{
				Type: &expr.Object{
					{
						Name: tc.fieldName,
						Attribute: &expr.AttributeExpr{
							Type: tc.fieldType,
						},
					},
				},
			}
			root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
				jsonrpcService(svc, "/rpc"),
			})
			mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
				Name:    "assistant-mcp",
				Version: "1.0.0",
				Resources: []*mcpexpr.ResourceExpr{
					{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
				},
			})

			err := PrepareServices("", []eval.Root{root})

			require.Error(t, err)
			require.ErrorContains(t, err, "read_document")
			require.ErrorContains(t, err, "resource query")
			require.ErrorContains(t, err, tc.fieldName)
		})
	}
}

func TestPrepareServices_RejectsResourcePayloadWithoutQueryableFields(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "read_document")
	methods["read_document"].Payload = &expr.AttributeExpr{Type: expr.String}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.Error(t, err)
	require.ErrorContains(t, err, "read_document")
	require.ErrorContains(t, err, "resource query")
	require.ErrorContains(t, err, "at least one")
}

func TestPrepareServices_AcceptsNotificationPayloadInheritedFromBase(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "send_notification")
	methods["send_notification"].Result = &expr.AttributeExpr{Type: expr.Empty}
	basePayload := &expr.UserTypeExpr{
		TypeName: "NotificationBase",
		AttributeExpr: &expr.AttributeExpr{
			Type: &expr.Object{
				{Name: "type", Attribute: &expr.AttributeExpr{Type: expr.String}},
				{Name: "message", Attribute: &expr.AttributeExpr{Type: expr.String}},
			},
			Validation: &expr.ValidationExpr{Required: []string{"type", "message"}},
		},
	}
	methods["send_notification"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "data", Attribute: &expr.AttributeExpr{Type: expr.Any}},
		},
		Bases: []expr.DataType{basePayload},
	}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Notifications: []*mcpexpr.NotificationExpr{
			{Name: "status_update", Method: methods["send_notification"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.NoError(t, err)
}

func TestPrepareServices_AcceptsNotificationPayloadDirectFieldsOverBase(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "send_notification")
	methods["send_notification"].Result = &expr.AttributeExpr{Type: expr.Empty}
	basePayload := &expr.UserTypeExpr{
		TypeName: "NotificationBase",
		AttributeExpr: &expr.AttributeExpr{
			Type: &expr.Object{
				{Name: "type", Attribute: &expr.AttributeExpr{Type: expr.Int}},
				{Name: "message", Attribute: &expr.AttributeExpr{Type: expr.String}},
			},
			Validation: &expr.ValidationExpr{Required: []string{"type", "message"}},
		},
	}
	methods["send_notification"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "type", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "data", Attribute: &expr.AttributeExpr{Type: expr.Any}},
		},
		Bases:      []expr.DataType{basePayload},
		Validation: &expr.ValidationExpr{Required: []string{"type"}},
	}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Notifications: []*mcpexpr.NotificationExpr{
			{Name: "status_update", Method: methods["send_notification"]},
		},
	})

	err := PrepareServices("", []eval.Root{root})

	require.NoError(t, err)
}

func TestPrepareServices_AcceptedPureMCPServiceAssignsEveryOriginalEndpoint(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService(
		"assistant",
		"analyze",
		"read_document",
		"generate_prompt",
		"send_notification",
	)
	methods["send_notification"].Payload = testNotificationPayload()
	methods["send_notification"].Result = &expr.AttributeExpr{Type: expr.Empty}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcp := &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "analyze", Method: methods["analyze"]},
		},
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
		},
		Notifications: []*mcpexpr.NotificationExpr{
			{Name: "status_update", Method: methods["send_notification"]},
		},
	}
	mcpexpr.Root.RegisterMCP(svc, mcp)
	mcpexpr.Root.DynamicPrompts[svc.Name] = []*mcpexpr.DynamicPromptExpr{
		{Name: "assistant_prompt", Method: methods["generate_prompt"]},
	}

	require.NoError(t, PrepareServices("", []eval.Root{root}))

	data, err := newAdapterGenerator(
		"example.com/assistant/gen",
		svc,
		mcp,
		newMCPExprBuilder(svc, mcp, nil, 0).BuildServiceMapping(),
		nil,
	).buildAdapterData()
	require.NoError(t, err)

	files := generateMCPClientAdapter("example.com/assistant/gen", svc, data)
	require.Len(t, files, 1)

	rendered := renderGeneratedFile(t, files[0])
	require.Contains(t, rendered, "func encodeOriginalPayload(")
	require.Contains(t, rendered, "func decodeOriginalJSONRPCResult(")
	require.NotContains(t, rendered, "reqArgs, _ :=")
	require.NotContains(t, rendered, "req3, _ :=")
	require.Contains(t, rendered, "e.Analyze =")
	require.Contains(t, rendered, "e.ReadDocument =")
	require.Contains(t, rendered, "e.GeneratePrompt =")
	require.Contains(t, rendered, "e.SendNotification =")
}

func TestGenerate_FailsWhenOriginalServiceHasNoJSONRPCPath(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "analyze")
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "analyze", Method: methods["analyze"]},
		},
	})

	_, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, `service "assistant" must declare JSONRPC`)
}

func TestPrepareServices_RejectsUnsupportedPureMCPMethodKinds(t *testing.T) {
	testCases := []struct {
		name string
		mcp  *mcpexpr.MCPExpr
	}{
		{
			name: "subscription",
			mcp: &mcpexpr.MCPExpr{
				Name:    "watcher",
				Version: "1.0.0",
				Subscriptions: []*mcpexpr.SubscriptionExpr{
					{
						ResourceName: "documents",
					},
				},
			},
		},
		{
			name: "subscription monitor",
			mcp: &mcpexpr.MCPExpr{
				Name:    "watcher",
				Version: "1.0.0",
				SubscriptionMonitors: []*mcpexpr.SubscriptionMonitorExpr{
					{
						Name: "events_stream",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			restore := resetMCPCodegenState(t)
			defer restore()

			svc, methods := testService("watcher", "watch_documents")
			switch tc.name {
			case "subscription":
				tc.mcp.Subscriptions[0].Method = methods["watch_documents"]
			case "subscription monitor":
				tc.mcp.SubscriptionMonitors[0].Method = methods["watch_documents"]
			}

			root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
				jsonrpcService(svc, "/rpc"),
			})
			mcpexpr.Root.RegisterMCP(svc, tc.mcp)

			err := PrepareServices("", []eval.Root{root})

			require.Error(t, err)
			require.ErrorContains(t, err, `service "watcher"`)
			require.ErrorContains(t, err, "watch_documents")
		})
	}
}

func TestPrepareExample_OnlyMountsMCPOnOriginalServers(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	alpha, alphaMethods := testService("alpha", "list")
	beta, _ := testService("beta", "status")
	root := &expr.RootExpr{
		Services: []*expr.ServiceExpr{alpha, beta},
		API: &expr.APIExpr{
			HTTP: &expr.HTTPExpr{
				Services: []*expr.HTTPServiceExpr{
					httpService(alpha),
					httpService(beta),
				},
			},
			JSONRPC: &expr.JSONRPCExpr{
				HTTPExpr: expr.HTTPExpr{
					Services: []*expr.HTTPServiceExpr{
						jsonrpcService(alpha, "/rpc"),
						jsonrpcService(beta, "/rpc"),
					},
				},
			},
			Servers: []*expr.ServerExpr{
				{Name: "alpha-server", Services: []string{"alpha"}},
				{Name: "beta-server", Services: []string{"beta"}},
			},
		},
	}
	mcpexpr.Root.RegisterMCP(alpha, &mcpexpr.MCPExpr{
		Name:    "alpha",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "list", Method: alphaMethods["list"]},
		},
	})

	err := PrepareExample("", []eval.Root{root})

	require.NoError(t, err)
	require.True(t, slices.Contains(root.API.Servers[0].Services, "mcp_alpha"))
	require.False(t, slices.Contains(root.API.Servers[0].Services, "alpha"))
	require.False(t, slices.Contains(root.API.Servers[1].Services, "mcp_alpha"))
	require.True(t, slices.Contains(root.API.Servers[1].Services, "beta"))
}

func resetMCPCodegenState(t *testing.T) func() {
	t.Helper()

	previousRoot := mcpexpr.Root
	mcpexpr.Root = mcpexpr.NewRoot()

	return func() {
		mcpexpr.Root = previousRoot
	}
}

func generateToolDiscoveryFixture(t *testing.T) []*gcodegen.File {
	t.Helper()
	restore := resetMCPCodegenState(t)
	t.Cleanup(restore)

	svc, methods := testService("assistant", "search")
	methods["search"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "query", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Search query"}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"query"}},
	}
	methods["search"].Result = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "summary", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Search summary"}},
			{Name: "score", Attribute: &expr.AttributeExpr{Type: expr.Float64, Description: "Search score"}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"summary"}},
	}
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{
				Name:              "search",
				Description:       "Search indexed content",
				Title:             "Search Content",
				DiscoveryCategory: "knowledge",
				DiscoveryTags:     []string{"search", "retrieval"},
				DiscoveryKeywords: []string{"lookup", "documents"},
				Method:            methods["search"],
			},
		},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	return files
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func testService(name string, methodNames ...string) (*expr.ServiceExpr, map[string]*expr.MethodExpr) {
	svc := &expr.ServiceExpr{Name: name}
	methods := make(map[string]*expr.MethodExpr, len(methodNames))
	for _, methodName := range methodNames {
		method := &expr.MethodExpr{
			Name:             methodName,
			Service:          svc,
			Payload:          &expr.AttributeExpr{Type: expr.Empty},
			Result:           &expr.AttributeExpr{Type: expr.String},
			StreamingPayload: &expr.AttributeExpr{Type: expr.Empty},
			StreamingResult:  &expr.AttributeExpr{Type: expr.Empty},
		}
		svc.Methods = append(svc.Methods, method)
		methods[methodName] = method
	}
	return svc, methods
}

func testNotificationPayload() *expr.AttributeExpr {
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "type", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "message", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "data", Attribute: &expr.AttributeExpr{Type: expr.Any}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"type"}},
	}
}

func testResourceQueryPayload() *expr.AttributeExpr {
	baseQuery := &expr.UserTypeExpr{
		TypeName: "ResourceQueryBase",
		AttributeExpr: &expr.AttributeExpr{
			Type: &expr.Object{
				{Name: "tenant", Attribute: &expr.AttributeExpr{Type: expr.String}},
			},
			Validation: &expr.ValidationExpr{Required: []string{"tenant"}},
		},
	}
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "cursor", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "offset", Attribute: &expr.AttributeExpr{Type: expr.Int}},
			{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.UInt, DefaultValue: 25}},
			{Name: "enabled", Attribute: &expr.AttributeExpr{Type: expr.Boolean}},
			{Name: "ratio", Attribute: &expr.AttributeExpr{Type: expr.Float64}},
			{
				Name: "tags",
				Attribute: &expr.AttributeExpr{
					Type: &expr.Array{
						ElemType: &expr.AttributeExpr{Type: expr.String},
					},
				},
			},
		},
		Bases:      []expr.DataType{baseQuery},
		Validation: &expr.ValidationExpr{Required: []string{"cursor"}},
	}
}

func testRootExpr(services []*expr.ServiceExpr, jsonrpcServices []*expr.HTTPServiceExpr) *expr.RootExpr {
	httpServices := make([]*expr.HTTPServiceExpr, 0, len(services))
	servers := make([]*expr.ServerExpr, 0, len(services))
	for _, svc := range services {
		httpServices = append(httpServices, httpService(svc))
		servers = append(servers, &expr.ServerExpr{
			Name:     svc.Name + "-server",
			Services: []string{svc.Name},
		})
	}
	return &expr.RootExpr{
		Services: services,
		API: &expr.APIExpr{
			ExampleGenerator: &expr.ExampleGenerator{
				Randomizer: expr.NewFakerRandomizer("mcp-contract-test"),
			},
			HTTP: &expr.HTTPExpr{Services: httpServices},
			JSONRPC: &expr.JSONRPCExpr{
				HTTPExpr: expr.HTTPExpr{Services: jsonrpcServices},
			},
			GRPC:    &expr.GRPCExpr{},
			Servers: servers,
		},
	}
}

func httpService(svc *expr.ServiceExpr) *expr.HTTPServiceExpr {
	return &expr.HTTPServiceExpr{ServiceExpr: svc}
}

func jsonrpcService(svc *expr.ServiceExpr, path string) *expr.HTTPServiceExpr {
	return jsonrpcServiceWithMethod(svc, path, http.MethodPost)
}

func jsonrpcServiceWithMethod(svc *expr.ServiceExpr, path string, method string) *expr.HTTPServiceExpr {
	return &expr.HTTPServiceExpr{
		ServiceExpr: svc,
		JSONRPCRoute: &expr.RouteExpr{
			Method: method,
			Path:   path,
		},
	}
}

func findGeneratedFile(t *testing.T, files []*gcodegen.File, path string) *gcodegen.File {
	t.Helper()
	want := filepath.ToSlash(path)
	for _, file := range files {
		if filepath.ToSlash(file.Path) == want {
			return file
		}
	}
	require.Failf(t, "generated file not found", "missing %s", want)
	return nil
}

func renderGeneratedFile(t *testing.T, file *gcodegen.File) string {
	t.Helper()

	var output bytes.Buffer
	for _, section := range file.AllSections() {
		switch sec := section.(type) {
		case *gcodegen.SectionTemplate:
			tmpl := template.New(sec.Name).Funcs(template.FuncMap{
				"comment": gcodegen.Comment,
				"commandLine": func() string {
					return ""
				},
			})
			if sec.FuncMap != nil {
				tmpl = tmpl.Funcs(sec.FuncMap)
			}
			parsed, err := tmpl.Parse(sec.Source)
			require.NoError(t, err)

			var rendered bytes.Buffer
			err = parsed.Execute(&rendered, sec.Data)
			require.NoError(t, err)
			output.Write(rendered.Bytes())
		default:
			err := section.Write(&output)
			require.NoError(t, err, "render %s", section.SectionName())
		}
	}

	require.NotEmpty(t, output.String(), filepath.ToSlash(file.Path))
	return output.String()
}
