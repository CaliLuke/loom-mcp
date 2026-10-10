package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentStructuralResultsStoreOnlyRequiredContent(t *testing.T) {
	for _, kind := range []string{"request error", "activity error", "confirmation denial", "tool unavailable"} {
		t.Run(kind, func(t *testing.T) {
			store := &contentPathStore{Store: storeinmem.New()}
			bus := &recordingHooks{ch: make(chan hooks.Event, 64)}
			eng := engineinmem.New()
			if kind == "activity error" {
				eng = &contentFailedToolEngine{eng}
			}
			rt := New(WithEngine(eng), WithRunStore(store), WithHooks(bus))
			spec := newAnyJSONSpec("structural.tool", "structural")
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "structural", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
				t.Error("structural result must not execute the tool")
				return nil, nil
			}}))
			if kind == "confirmation denial" {
				rt.toolConfirmation = &ToolConfirmationConfig{Confirm: map[tools.Ident]*ToolConfirmation{spec.Name: {Prompt: func(context.Context, *planner.ToolRequest) (string, error) {
					return "confirm", nil
				}, DeniedResult: func(context.Context, *planner.ToolRequest) (any, error) { return map[string]bool{"denied": true}, nil }}}}
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				call := planner.ToolRequest{Name: spec.Name, ToolCallID: "structural", Payload: rawjson.Message(`{}`)}
				if kind == "request error" {
					call.Error = planner.NewToolError("invalid arguments")
				}
				if kind == "tool unavailable" {
					call.Name = "not.registered"
				}
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{call}}, nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				require.Len(t, in.ToolOutputs, 1)
				assert.Empty(t, in.ToolOutputs[0].Result)
				if kind == "confirmation denial" {
					assert.NotEmpty(t, in.ToolOutputs[0].Refs.Result)
				} else {
					assert.Empty(t, in.ToolOutputs[0].Refs.Result)
				}
				assert.NotEmpty(t, store.snapshot())
				return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("done")}}, nil
			}})
			handle, err := rt.MustClient("content.agent").Start(t.Context(), "session", nil, WithRunID("root"))
			require.NoError(t, err)
			if kind == "confirmation denial" {
				for event := range bus.ch {
					if awaiting, ok := event.(*hooks.AwaitConfirmationEvent); ok {
						require.NoError(t, rt.ProvideConfirmation(t.Context(), &api.ConfirmationDecision{RunID: "root", ID: awaiting.ID, Approved: false, RequestedBy: "test"}))
						break
					}
				}
			}
			_, err = handle.Wait(t.Context())
			require.NoError(t, err)
		})
	}
}

// contentFailedToolEngine fails execution before invoking the tool handler.
type contentFailedToolEngine struct{ engine.Engine }

func (e *contentFailedToolEngine) RegisterExecuteToolActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.ToolInput) (*api.ToolOutput, error)) error {
	return e.Engine.RegisterExecuteToolActivity(ctx, name, opts, func(context.Context, *api.ToolInput) (*api.ToolOutput, error) {
		return nil, errors.New("activity failed")
	})
}
