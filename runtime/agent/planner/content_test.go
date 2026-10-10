package planner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

type graphContent map[runstore.Hash]rawjson.Message

func referencedGraphInput(t *testing.T, in *PlanResumeInput) *PlanResumeInput {
	t.Helper()
	owned := *in
	in = &owned
	in.ToolOutputs = append([]*ToolOutput(nil), in.ToolOutputs...)
	for i, out := range in.ToolOutputs {
		if out != nil {
			copy := *out
			in.ToolOutputs[i] = &copy
		}
	}
	in.TypedInputs = append([]TypedInputOutput(nil), in.TypedInputs...)
	content := graphContent{}
	for _, out := range in.ToolOutputs {
		if out == nil || len(out.Result) == 0 {
			continue
		}
		out.Refs.Result = runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(out.Result)))
		content[out.Refs.Result] = out.Result
		out.Result = nil
	}
	for i := range in.TypedInputs {
		out := &in.TypedInputs[i]
		out.PayloadRef = runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(out.Payload)))
		content[out.PayloadRef] = out.Payload
		out.Payload = nil
	}
	in.Content = content
	return in
}

func (c graphContent) LoadToolResult(ctx context.Context, refs tools.ResultRefs) (rawjson.Message, error) {
	return c.LoadTypedInput(ctx, refs.Result)
}
func (c graphContent) LoadServerData(ctx context.Context, refs tools.ResultRefs) (rawjson.Message, error) {
	return c.LoadTypedInput(ctx, refs.ServerData)
}
func (c graphContent) LoadToolPayload(ctx context.Context, output *ToolOutput) (rawjson.Message, error) {
	return c.LoadTypedInput(ctx, output.Refs.Payload)
}
func (c graphContent) LoadTypedInput(_ context.Context, hash runstore.Hash) (rawjson.Message, error) {
	data, ok := c[hash]
	if !ok {
		return nil, runstore.ErrNotFound
	}
	return data, nil
}

func TestGraphPredicateRequiresLoadableReferences(t *testing.T) {
	p := NewGraphWorkflowPlanner(WorkflowGraphConfig{Nodes: []WorkflowNode{
		{ID: "source", Kind: WorkflowNodeTool, Tool: "svc.read"},
		{ID: "branch", Kind: WorkflowNodeBranch, DependsOn: []string{"source"}, Branch: &WorkflowBranchConfig{FromStep: "source"}},
	}})
	in := &PlanResumeInput{ToolOutputs: []*ToolOutput{{ToolCallID: "source"}}}
	_, err := p.PlanResume(t.Context(), in)
	require.ErrorContains(t, err, "content loader")
	in.Content = graphContent{}
	_, err = p.PlanResume(t.Context(), in)
	require.ErrorIs(t, err, runstore.ErrNotFound)
}
