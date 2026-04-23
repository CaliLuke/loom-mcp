package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

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
