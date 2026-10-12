package runtime

import (
	"context"
	"testing"
	"time"

	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvideToolResultsAcceptsReusedAwaitIDAcrossTurns(t *testing.T) {
	for _, kind := range []string{"questions", "external"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			eng := &rb8Engine{Engine: engineinmem.New()}
			bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
			rt := New(WithEngine(eng), WithHooks(bus), WithRunStore(storeinmem.New()))
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{providedValidationSpec()}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
				panic("external tool must not execute")
			}}))
			awaitFor := func(call string) *planner.PlanResult {
				item := rb7Await(kind, call)
				if kind == "questions" {
					item.Questions.ID = "ask"
				} else {
					item.ExternalTools.ID = "ask"
				}
				return &planner.PlanResult{Await: planner.NewAwait(item)}
			}
			resumes := 0
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return awaitFor("c1"), nil
			}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
				resumes++
				if resumes == 1 {
					return awaitFor("c2"), nil
				}
				return oracleFinal(), nil
			}})
			handle, err := rt.MustClient("content.agent").Start(ctx, "session", nil, WithRunID("root"))
			require.NoError(t, err)

			for _, call := range []string{"c1", "c2"} {
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					awaits, queryErr := eng.QueryToolAwaits(ctx, "root")
					assert.NoError(c, queryErr)
					if assert.Len(c, awaits, 1) && assert.Len(c, awaits[0].Items, 1) {
						assert.Equal(c, "ask", awaits[0].ID)
						assert.Equal(c, call, awaits[0].Items[0].ToolCallID)
					}
				}, time.Second, time.Millisecond)
				answer := rb7Answer(call)
				answer.ID = "ask"
				require.NoError(t, rt.ProvideToolResults(ctx, answer), "the current head must accept a reused await ID")
			}
			out, err := handle.Wait(ctx)
			require.NoError(t, err, "an accepted answer for the reused current await must complete the run")
			require.NotNil(t, out)
			assert.Equal(t, contentMessage("done"), out.Final)
			assert.Equal(t, 2, resumes)
			assert.Equal(t, int32(2), eng.signals.Load())
			assert.Equal(t, int32(2), eng.records.Load())
			events, err := LoadRunToolEvents(ctx, rt.RunStore, "root", out.ToolEventsRef)
			require.NoError(t, err)
			require.Len(t, events, 2)
			assert.Equal(t, "c1", events[0].ToolCallID)
			assert.Equal(t, "c2", events[1].ToolCallID)
		})
	}
}
