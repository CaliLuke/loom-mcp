package longrun

import (
	"context"
	"fmt"
	"strings"

	"example.com/longrun/fake"
	"example.com/longrun/gen/longrun/toolsets/dispatch_planner"
	"example.com/longrun/gen/longrun/toolsets/ledger_clerk"
	"example.com/longrun/gen/longrun/toolsets/ops_deployer"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Planner forwards model tool calls and final messages without scoring knowledge.
type Planner struct{}

// PlanStart invokes the configured planner model.
func (p *Planner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	return p.turn(ctx, in.Agent, in.Messages)
}

// PlanResume invokes the same model with the adopted tool results.
func (p *Planner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.turn(ctx, in.Agent, in.Messages)
}

func (p *Planner) turn(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	id := "planner"
	text, err := fake.RequestText(&model.Request{Messages: messages})
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, "[[switch-model]]") {
		id = "planner_small"
	}
	client, ok := pc.ModelClient(id)
	if !ok {
		return nil, fmt.Errorf("missing model %s", id)
	}
	var specs []tools.ToolSpec
	switch {
	case strings.Contains(string(pc.ID()), "ops_deployer"):
		specs = ops_deployer.Specs
	case strings.Contains(string(pc.ID()), "ledger_clerk"):
		specs = ledger_clerk.Specs
	case strings.Contains(string(pc.ID()), "dispatch_planner"):
		specs = dispatch_planner.Specs
	default:
		return nil, fmt.Errorf("unknown task agent %s", pc.ID())
	}
	defs := make([]*model.ToolDefinition, 0, len(specs))
	for _, s := range specs {
		schema, err := model.DecodeSchemaJSON(s.Payload.Schema)
		if err != nil {
			return nil, err
		}
		defs = append(defs, &model.ToolDefinition{Name: s.Name.String(), Description: s.Description, InputSchema: schema})
	}
	resp, err := client.Complete(ctx, &model.Request{Messages: messages, Tools: defs})
	if err != nil {
		return nil, err
	}
	out := &planner.PlanResult{}
	for _, c := range resp.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, planner.ToolRequest{Name: c.Name, ToolCallID: c.ID, Payload: c.Payload})
	}
	if len(out.ToolCalls) == 0 {
		if len(resp.Content) == 0 {
			return nil, fmt.Errorf("empty model response")
		}
		out.FinalResponse = &planner.FinalResponse{Message: &resp.Content[0]}
	}
	return out, nil
}
