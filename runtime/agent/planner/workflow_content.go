package planner

import (
	"context"
	"fmt"
)

// predicateInputs loads only values examined by graph branch/loop predicates.
// Completion tracking continues to use reference-only metadata.
func (p *GraphWorkflowPlanner) predicateInputs(ctx context.Context, input *PlanResumeInput) ([]*ToolOutput, []TypedInputOutput, error) {
	needed := make(map[string]bool)
	for _, node := range p.nodes {
		if node.Branch != nil {
			needed[node.Branch.FromStep] = true
		}
		if node.Loop != nil && node.Loop.Until != nil {
			needed[node.Loop.Until.Step] = true
		}
	}
	outputs := append([]*ToolOutput(nil), input.ToolOutputs...)
	for i, output := range outputs {
		if output == nil || output.Error != nil || !needed[graphNodeIDForToolCallID(output.ToolCallID)] {
			continue
		}
		if input.Content == nil {
			return nil, nil, fmt.Errorf("workflow predicate requires a content loader")
		}
		data, err := input.Content.LoadToolResult(ctx, output.Refs)
		if err != nil {
			return nil, nil, err
		}
		copy := *output
		copy.Result = data
		outputs[i] = &copy
	}
	typed := append([]TypedInputOutput(nil), input.TypedInputs...)
	for i := range typed {
		if !needed[typed[i].ID] {
			continue
		}
		if input.Content == nil {
			return nil, nil, fmt.Errorf("workflow predicate requires a content loader")
		}
		data, err := input.Content.LoadTypedInput(ctx, typed[i].PayloadRef)
		if err != nil {
			return nil, nil, err
		}
		typed[i].Payload = data
	}
	return outputs, typed, nil
}
