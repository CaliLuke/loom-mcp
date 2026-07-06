package planner

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// WorkflowStep describes one deterministic tool step in a composed workflow.
	WorkflowStep struct {
		// Name is the stable step identifier. It is used as ToolCallID when set.
		Name string
		// Tool is the canonical tool identifier to call for this step.
		Tool tools.Ident
		// Payload is the raw JSON payload passed to the tool.
		Payload rawjson.Message
	}

	// SequentialWorkflowConfig configures a model-free sequential workflow planner.
	SequentialWorkflowConfig struct {
		Steps []WorkflowStep
		// FinalMessage is emitted after all steps complete.
		FinalMessage string
	}

	// SequentialWorkflowPlanner runs fixed tool steps one at a time and finalizes
	// after all step outputs have been observed.
	SequentialWorkflowPlanner struct {
		steps        []WorkflowStep
		finalMessage string
	}
)

// NewSequentialWorkflowPlanner constructs a deterministic planner for simple
// workflow composition. It is useful for generated or handwritten coordinator
// agents that need fixed tool ordering without an LLM decision on each step.
func NewSequentialWorkflowPlanner(cfg SequentialWorkflowConfig) *SequentialWorkflowPlanner {
	return &SequentialWorkflowPlanner{
		steps:        append([]WorkflowStep(nil), cfg.Steps...),
		finalMessage: cfg.FinalMessage,
	}
}

// PlanStart emits the first workflow step.
func (p *SequentialWorkflowPlanner) PlanStart(_ context.Context, _ *PlanInput) (*PlanResult, error) {
	return p.stepResult(0)
}

// PlanResume emits the next workflow step or the final response when all steps
// have completed.
func (p *SequentialWorkflowPlanner) PlanResume(_ context.Context, input *PlanResumeInput) (*PlanResult, error) {
	if input == nil {
		return nil, errors.New("plan resume input is required")
	}
	return p.stepResult(len(input.ToolOutputs))
}

func (p *SequentialWorkflowPlanner) stepResult(index int) (*PlanResult, error) {
	if len(p.steps) == 0 {
		return nil, errors.New("sequential workflow requires at least one step")
	}
	if index < len(p.steps) {
		call, err := p.toolRequest(p.steps[index], index)
		if err != nil {
			return nil, err
		}
		return &PlanResult{ToolCalls: []ToolRequest{call}}, nil
	}
	return &PlanResult{
		FinalResponse: &FinalResponse{
			Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: p.finalMessage}},
			},
		},
	}, nil
}

func (p *SequentialWorkflowPlanner) toolRequest(step WorkflowStep, index int) (ToolRequest, error) {
	if step.Tool == "" {
		return ToolRequest{}, fmt.Errorf("workflow step %d requires tool", index)
	}
	id := step.Name
	if id == "" {
		id = fmt.Sprintf("step-%d", index+1)
	}
	return ToolRequest{
		Name:       step.Tool,
		Payload:    step.Payload,
		ToolCallID: id,
	}, nil
}
