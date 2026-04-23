package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
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

	promptPayload, err := r.validateAgentToolRequest(ctx, cfg, call, messages, parentRun)
	if err != nil {
		return nil, zeroCtx, err
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
