package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
)

// defaultAgentToolExecute returns the standard Execute function for agent-as-tool registrations.
func defaultAgentToolExecute(rt *Runtime, cfg AgentToolConfig) func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
	return func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		wfCtx := engine.WorkflowContextFromContext(ctx)
		if wfCtx == nil {
			return nil, fmt.Errorf("workflow context not found")
		}
		if cfg.Route.ID == "" || cfg.Route.WorkflowName == "" || cfg.Route.DefaultTaskQueue == "" {
			return nil, fmt.Errorf("agent tool route is required")
		}
		parentRun := &run.Context{
			RunID:     call.RunID,
			RootRunID: call.RootRunID,
			SessionID: call.SessionID,
			TurnID:    call.TurnID,
		}
		failure, err := rt.validateChildDispatch(wfCtx, call.AgentID, &cfg, call, parentRun, nil)
		if err != nil {
			return nil, err
		}
		if failure != nil {
			return Executed(failure), nil
		}
		messages, nestedRunCtx, err := rt.buildAgentChildRequest(wfCtx.Context(), &cfg, call)
		if err != nil {
			result, ferr := rt.agentToolRequestFailureResult(*call, err)
			if ferr != nil {
				return nil, ferr
			}
			return Executed(result), nil
		}
		return rt.executeValidatedAgentChild(ctx, wfCtx, &cfg, call, messages, nestedRunCtx)
	}
}

// executeValidatedAgentChild owns admission and execution only after request
// construction and the recorded pre-child validator have succeeded.
func (r *Runtime) executeValidatedAgentChild(ctx context.Context, wfCtx engine.WorkflowContext, cfg *AgentToolConfig, call *planner.ToolRequest, messages []*model.Message, nestedRunCtx run.Context) (*ToolExecutionResult, error) {
	if err := r.admitAgentChild(wfCtx, *call, nestedRunCtx, cfg.AgentID); err != nil {
		return nil, err
	}
	outPtr, err := r.executeReservedAgentChild(wfCtx, cfg.Route, messages, nestedRunCtx, ctx)
	if err != nil {
		return nil, fmt.Errorf("execute agent: %w", err)
	}
	result, err := r.adaptAgentChildOutput(ctx, cfg, call, nestedRunCtx, outPtr)
	if err != nil {
		return nil, err
	}
	return Executed(result), nil
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
func (r *Runtime) buildAgentChildRequest(ctx context.Context, cfg *AgentToolConfig, call *planner.ToolRequest) ([]*model.Message, run.Context, error) {
	var zeroCtx run.Context

	promptPayload, err := r.decodeAgentToolPromptPayload(ctx, *call)
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
		RootRunID:        call.RootRunID,
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
