type localToolCallCollector struct {
	parts     []*ToolsCallResult
	final     *ToolsCallResult
	streamErr error
}

// {{ .ConstructorName }} creates an in-process runtime registration backed by
// the adapter's progressive-discovery catalog and dispatch paths.
func {{ .ConstructorName }}(adapter *MCPAdapter) (agentsruntime.ToolsetRegistration, error) {
	if adapter == nil {
		return agentsruntime.ToolsetRegistration{}, errors.New("MCP adapter is required")
	}
	if !adapter.toolSearchEnabled() {
		return agentsruntime.ToolsetRegistration{}, errors.New("MCP adapter ToolSearch options are required")
	}
	specs, err := localProgressiveToolSpecs(adapter)
	if err != nil {
		return agentsruntime.ToolsetRegistration{}, err
	}
	return agentsruntime.ToolsetRegistration{
		Name:             {{ printf "%q" .SuiteQualifiedName }},
		Description:      {{ printf "%q" .Description }},
		Specs:            specs,
		DecodeInExecutor: true,
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
			if call == nil {
				return nil, errors.New("tool request is nil")
			}
			return executeLocalProgressiveTool(ctx, adapter, call)
		},
	}, nil
}

func executeLocalProgressiveTool(ctx context.Context, adapter *MCPAdapter, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
	toolName := string(call.Name)
	const suitePrefix = {{ printf "%q" .SuiteQualifiedName }} + "."
	if strings.HasPrefix(toolName, suitePrefix) {
		toolName = toolName[len(suitePrefix):]
	}
	arguments := bytes.TrimSpace(call.Payload.RawMessage())
	if len(arguments) == 0 || bytes.Equal(arguments, []byte("null")) {
		arguments = []byte("{}")
	}
	payload := &ToolsCallPayload{
		Name:      toolName,
		Arguments: mcpJSONFromRaw(append(jsontext.Value(nil), arguments...)),
	}
	ctx = mcpruntime.WithProjectedToolCallMeta(ctx, agentsruntime.ToolCallMeta{
		RunID:            call.RunID,
		SessionID:        call.SessionID,
		TurnID:           call.TurnID,
		ToolCallID:       call.ToolCallID,
		ParentToolCallID: call.ParentToolCallID,
	})
	response, err := adapter.executeLocalProgressiveTool(ctx, payload)
	if err != nil {
		return agentsruntime.Executed(&planner.ToolResult{
			Name:       call.Name,
			ToolCallID: call.ToolCallID,
			Error:      planner.ToolErrorFromError(err),
		}), nil
	}
	result, err := localPlannerToolResult(call, response)
	if err != nil {
		return nil, err
	}
	return agentsruntime.Executed(result), nil
}

func (a *MCPAdapter) executeLocalProgressiveTool(ctx context.Context, payload *ToolsCallPayload) (*ToolsCallResult, error) {
	arguments, err := mcpJSONRaw(payload.Arguments)
	if err != nil {
		return nil, err
	}
	info := a.toolCallInfo(payload, arguments)
	handler := a.wrapToolCallHandler(info, a.collectLocalProgressiveTool)
	return handler(ctx, payload)
}

func (a *MCPAdapter) collectLocalProgressiveTool(ctx context.Context, payload *ToolsCallPayload) (*ToolsCallResult, error) {
	collector := new(localToolCallCollector)
	if _, err := a.localProgressiveToolHandler(ctx, payload, collector); err != nil {
		return nil, err
	}
	return collector.result()
}

func (a *MCPAdapter) localProgressiveToolHandler(ctx context.Context, payload *ToolsCallPayload, stream toolCallStream) (bool, error) {
	name := ""
	if payload != nil {
		name = payload.Name
	}
	if a.isToolSearchName(name) {
		return a.handleSearchTools(ctx, payload, stream)
	}
	if a.isToolCallProxyName(name) {
		return a.handleCallToolProxy(ctx, payload, stream)
	}
	if !a.isAlwaysVisibleToolName(name) {
		return false, errors.New("unknown local progressive-discovery tool: " + name)
	}
	return a.executeRealTool(ctx, payload, stream)
}

func localProgressiveToolSpecs(adapter *MCPAdapter) ([]tools.ToolSpec, error) {
	catalog := adapter.toolSearchSyntheticTools()
	catalog = append(catalog, adapter.visibleToolCatalog(adapter.generatedToolCatalog())...)
	specs := make([]tools.ToolSpec, 0, len(catalog))
	for _, tool := range catalog {
		if tool == nil {
			continue
		}
		description := ""
		if tool.Description != nil {
			description = *tool.Description
		}
		example := toolCallArgumentsExample(tool)
		exampleJSON, err := json.Marshal(example)
		if err != nil {
			return nil, err
		}
		_, tags, _ := toolDiscoveryMetadata(tool)
		specs = append(specs, tools.ToolSpec{
			Name:        tools.Ident(tool.Name),
			Service:     {{ printf "%q" .ServiceName }},
			Toolset:     {{ printf "%q" .SuiteQualifiedName }},
			Description: description,
			Tags:        append([]string(nil), tags...),
			Payload: tools.TypeSpec{
				Name:         "any",
				Schema:       append([]byte(nil), toolRawJSON(tool.InputSchema)...),
				ExampleJSON:  exampleJSON,
				ExampleInput: example,
				Codec:        tools.AnyJSONCodec,
			},
			Result: tools.TypeSpec{
				Name:   "any",
				Schema: append([]byte(nil), toolRawJSON(tool.OutputSchema)...),
				Codec:  tools.AnyJSONCodec,
			},
		})
	}
	return specs, nil
}

func localPlannerToolResult(call *planner.ToolRequest, response *ToolsCallResult) (*planner.ToolResult, error) {
	result := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID}
	if response == nil {
		result.Result = map[string]any{}
		return result, nil
	}
	text := localToolContentText(response.Content)
	if response.IsError != nil && *response.IsError {
		if text == "" {
			text = "Tool execution failed."
		}
		result.Error = planner.NewToolError(text)
		return result, nil
	}
	structuredContent, err := mcpJSONRaw(response.StructuredContent)
	if err != nil {
		return nil, err
	}
	if len(structuredContent) > 0 {
		var structured any
		if err := json.Unmarshal(structuredContent, &structured); err != nil {
			return nil, err
		}
		result.Result = structured
		return result, nil
	}
	if text != "" {
		result.Result = text
	} else {
		result.Result = map[string]any{}
	}
	return result, nil
}

func localToolContentText(content []*ContentItem) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item != nil && item.Text != nil {
			parts = append(parts, *item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (c *localToolCallCollector) Send(_ context.Context, result *ToolsCallResult) error {
	c.parts = append(c.parts, result)
	return nil
}

func (c *localToolCallCollector) SendAndClose(_ context.Context, result *ToolsCallResult) error {
	c.final = result
	return nil
}

func (c *localToolCallCollector) SendError(_ context.Context, _ any, err error) error {
	c.streamErr = err
	return nil
}

func (c *localToolCallCollector) result() (*ToolsCallResult, error) {
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	if len(c.parts) == 0 {
		if c.final == nil {
			return &ToolsCallResult{}, nil
		}
		return c.final, nil
	}
	merged := new(ToolsCallResult)
	for _, part := range append(c.parts, c.final) {
		if part == nil {
			continue
		}
		merged.Content = append(merged.Content, part.Content...)
		if part.StructuredContent.Present() {
			merged.StructuredContent = part.StructuredContent
		}
		if part.IsError != nil {
			value := *part.IsError
			merged.IsError = &value
		}
	}
	return merged, nil
}
