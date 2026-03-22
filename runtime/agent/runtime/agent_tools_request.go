package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// defaultAgentToolExecute returns the standard Execute function for agent-as-tool registrations.
func defaultAgentToolExecute(rt *Runtime, cfg AgentToolConfig) func(context.Context, *planner.ToolRequest) (*planner.ToolResult, error) {
	return func(ctx context.Context, call *planner.ToolRequest) (*planner.ToolResult, error) {
		wfCtx := engine.WorkflowContextFromContext(ctx)
		if wfCtx == nil {
			return nil, fmt.Errorf("workflow context not found")
		}
		if cfg.Route.ID == "" {
			return nil, fmt.Errorf("agent tool route is required")
		}
		parentRun := &run.Context{
			RunID:     call.RunID,
			SessionID: call.SessionID,
			TurnID:    call.TurnID,
		}
		messages, nestedRunCtx, err := rt.buildAgentChildRequest(wfCtx.Context(), &cfg, call, nil, parentRun)
		if err != nil {
			return rt.agentToolRequestFailureResult(*call, err)
		}
		if err := rt.publishHook(
			wfCtx.Context(),
			hooks.NewChildRunLinkedEvent(
				call.RunID,
				call.AgentID,
				call.SessionID,
				call.Name,
				call.ToolCallID,
				nestedRunCtx.RunID,
				cfg.AgentID,
			),
			"",
		); err != nil {
			return nil, err
		}
		outPtr, err := rt.ExecuteAgentChildWithRoute(wfCtx, cfg.Route, messages, nestedRunCtx)
		if err != nil {
			return nil, fmt.Errorf("execute agent: %w", err)
		}
		return rt.adaptAgentChildOutput(ctx, &cfg, call, nestedRunCtx, outPtr)
	}
}

func (r *Runtime) agentToolRequestFailureResult(call planner.ToolRequest, err error) (*planner.ToolResult, error) {
	spec, ok := r.toolSpec(call.Name)
	if !ok {
		return nil, fmt.Errorf("agent tool %s requires a registered ToolSpec", call.Name)
	}
	hint := buildRetryHintFromAgentToolRequestError(err, call.Name, &spec)
	if hint == nil {
		return nil, err
	}
	toolErr := planner.NewToolError(err.Error())
	result := &planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      toolErr,
	}
	hint.RestrictToTool = true
	result.RetryHint = hint
	if _, err := r.materializeToolResult(context.Background(), call, result); err != nil {
		return nil, err
	}
	return result, nil
}

// buildAgentChildRequest constructs the nested agent messages and run context for an agent-as-tool invocation.
func (r *Runtime) buildAgentChildRequest(ctx context.Context, cfg *AgentToolConfig, call *planner.ToolRequest, messages []*model.Message, parentRun *run.Context) ([]*model.Message, run.Context, error) {
	var zeroCtx run.Context

	promptPayload, err := r.decodeAgentToolPromptPayload(ctx, *call)
	if err != nil {
		return nil, zeroCtx, err
	}
	if cfg.PreChildValidator != nil {
		if err := cfg.PreChildValidator(ctx, buildAgentToolValidationInput(call, promptPayload, messages, parentRun)); err != nil {
			return nil, zeroCtx, err
		}
	}

	var childMessages []*model.Message
	if cfg.SystemPrompt != "" {
		if m := newTextAgentMessage(model.ConversationRoleSystem, cfg.SystemPrompt); m != nil {
			childMessages = []*model.Message{m}
		}
	}

	userContent, err := r.renderAgentToolUserContent(ctx, cfg, call, promptPayload)
	if err != nil {
		return nil, zeroCtx, err
	}
	if m := newTextAgentMessage(model.ConversationRoleUser, userContent); m != nil {
		childMessages = append(childMessages, m)
	} else {
		childMessages = append(childMessages, &model.Message{Role: model.ConversationRoleUser})
	}

	nestedRunCtx := buildNestedAgentRunContext(*call)
	return childMessages, nestedRunCtx, nil
}

func (r *Runtime) decodeAgentToolPromptPayload(ctx context.Context, call planner.ToolRequest) (any, error) {
	if len(call.Payload) == 0 {
		return nil, nil
	}
	if _, ok := r.ToolSpec(call.Name); !ok {
		return nil, fmt.Errorf("agent tool %s requires a registered ToolSpec for payload decoding (missing specs/codecs)", call.Name)
	}
	val, err := r.unmarshalToolValue(ctx, call.Name, call.Payload.RawMessage(), true)
	if err != nil {
		return nil, fmt.Errorf("decode agent tool payload for %s: %w", call.Name, err)
	}
	return val, nil
}

func buildAgentToolValidationInput(call *planner.ToolRequest, promptPayload any, messages []*model.Message, parentRun *run.Context) *AgentToolValidationInput {
	return &AgentToolValidationInput{
		Call:      call,
		Payload:   promptPayload,
		Messages:  messages,
		ParentRun: parentRun,
	}
}

func (r *Runtime) renderAgentToolUserContent(ctx context.Context, cfg *AgentToolConfig, call *planner.ToolRequest, promptPayload any) (string, error) {
	if promptID, ok := cfg.PromptSpecs[call.Name]; ok {
		rendered, err := r.renderAgentToolPrompt(ctx, promptID, call, promptPayload)
		if err != nil {
			return "", fmt.Errorf("render prompt for %s: %w", call.Name, err)
		}
		return rendered, nil
	}
	if tmpl := cfg.Templates[call.Name]; tmpl != nil {
		var b strings.Builder
		if err := tmpl.Execute(&b, promptPayload); err != nil {
			return "", fmt.Errorf("render tool template for %s: %w", call.Name, err)
		}
		return b.String(), nil
	}
	if txt, ok := cfg.Texts[call.Name]; ok {
		return txt, nil
	}
	if cfg.Prompt != nil {
		return cfg.Prompt(call.Name, promptPayload), nil
	}
	if len(call.Payload) > 0 {
		return string(call.Payload.RawMessage()), nil
	}
	return "", nil
}

func buildNestedAgentRunContext(call planner.ToolRequest) run.Context {
	nestedRunCtx := run.Context{
		Tool:             call.Name,
		RunID:            NestedRunIDForToolCall(call.RunID, call.Name, call.ToolCallID),
		SessionID:        call.SessionID,
		TurnID:           call.TurnID,
		ParentToolCallID: call.ToolCallID,
		ParentRunID:      call.RunID,
		ParentAgentID:    call.AgentID,
		Labels:           cloneLabels(call.Labels),
	}
	if len(call.Payload) > 0 {
		nestedRunCtx.ToolArgs = append(rawjson.Message(nil), call.Payload...)
	}
	return nestedRunCtx
}

// renderAgentToolPrompt resolves and renders a configured prompt spec for one tool call.
func (r *Runtime) renderAgentToolPrompt(ctx context.Context, promptID prompt.Ident, call *planner.ToolRequest, payload any) (string, error) {
	if promptID == "" {
		return "", errors.New("prompt id is required")
	}
	if r.PromptRegistry == nil {
		return "", errors.New("prompt registry is not configured")
	}
	renderData, err := r.buildPromptTemplateData(ctx, call.Name, payload)
	if err != nil {
		return "", fmt.Errorf("build prompt template data for %s: %w", call.Name, err)
	}
	renderContext := withPromptRenderHookContext(ctx, PromptRenderHookContext{
		RunID:     call.RunID,
		AgentID:   call.AgentID,
		SessionID: call.SessionID,
		TurnID:    call.TurnID,
	})
	content, err := r.PromptRegistry.Render(renderContext, promptID, prompt.Scope{
		SessionID: call.SessionID,
		Labels:    cloneLabels(call.Labels),
	}, renderData)
	if err != nil {
		return "", err
	}
	return content.Text, nil
}

// buildPromptTemplateData converts a typed tool payload into canonical prompt template data.
func (r *Runtime) buildPromptTemplateData(ctx context.Context, toolName tools.Ident, payload any) (map[string]any, error) {
	if payload == nil {
		return map[string]any{}, nil
	}
	switch payload.(type) {
	case json.RawMessage, []byte:
		return nil, fmt.Errorf("tool %s prompt payload must be a typed Go value, got %T", toolName, payload)
	}
	codec, ok := r.toolCodec(toolName, true)
	if !ok || codec.ToJSON == nil {
		r.logger.Error(ctx, "no codec found for tool", "tool", toolName, "payload", true)
		return nil, fmt.Errorf("no codec found for tool %s", toolName)
	}
	raw, err := codec.ToJSON(payload)
	if err != nil {
		r.logger.Warn(ctx, "tool codec encode failed", "tool", toolName, "payload", true, "err", err)
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool %s prompt payload must render from a JSON object, got %T", toolName, decoded)
	}
	return object, nil
}
