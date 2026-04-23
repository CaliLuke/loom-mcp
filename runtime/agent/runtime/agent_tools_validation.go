package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
)

func (r *Runtime) validateAgentToolRequest(ctx context.Context, cfg *AgentToolConfig, call *planner.ToolRequest, messages []*model.Message, parentRun *run.Context) (any, error) {
	promptPayload, err := r.decodeAgentToolPromptPayload(ctx, *call)
	if err != nil {
		return nil, err
	}
	if cfg.PreChildValidator == nil {
		return promptPayload, nil
	}
	if err := cfg.PreChildValidator(ctx, buildAgentToolValidationInput(call, promptPayload, messages, parentRun)); err != nil {
		return nil, err
	}
	return promptPayload, nil
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
