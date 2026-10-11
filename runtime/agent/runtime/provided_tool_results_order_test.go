package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvideToolResultsRejectsLaterAwaitUntilHeadConsumed(t *testing.T) {
	for _, kinds := range [][2]string{{"questions", "questions"}, {"external", "external"}, {"questions", "external"}, {"external", "questions"}} {
		t.Run(kinds[0]+"/"+kinds[1], func(t *testing.T) {
			eng := engineinmem.New()
			store := &contentPathStore{Store: storeinmem.New()}
			bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
			rt := New(WithEngine(eng), WithRunStore(store), WithHooks(bus))
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{providedValidationSpec()}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
				panic("external tool must not execute")
			}}))
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{Await: planner.NewAwait(rb7Await(kinds[0], "first"), rb7Await(kinds[1], "second"))}, nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				require.Len(t, in.ToolOutputs, 2)
				assert.Equal(t, "first", in.ToolOutputs[0].ToolCallID)
				assert.Equal(t, "second", in.ToolOutputs[1].ToolCallID)
				var sequence []string
				for _, message := range in.Messages {
					for _, part := range message.Parts {
						switch p := part.(type) {
						case model.ToolUsePart:
							sequence = append(sequence, "use:"+p.ID)
						case model.ToolResultPart:
							sequence = append(sequence, "result:"+p.ToolUseID)
						}
					}
				}
				assert.Equal(t, []string{"use:first", "result:first", "use:second", "result:second"}, sequence)
				return oracleFinal(), nil
			}})
			runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
				if event.Type() != hooks.RunPaused {
					return nil
				}
				before := store.snapshot()
				err := rt.ProvideToolResults(t.Context(), rb7Answer("second"))
				require.Error(t, err, "a later answer must be rejected before it reaches the shared signal channel")
				assertToolResultContractError(t, err)
				assert.Contains(t, err.Error(), "not yet awaited")
				var notReady *ToolAwaitNotReadyError
				require.ErrorAs(t, err, &notReady)
				assert.Equal(t, "second", notReady.ID)
				assert.Equal(t, before, store.snapshot())
				snapshot, err := rt.GetRunSnapshot(t.Context(), "root")
				require.NoError(t, err)
				assert.Equal(t, run.StatusPaused, snapshot.Status)
				awaits, err := eng.(engine.ToolAwaitQuerier).QueryToolAwaits(t.Context(), "root")
				require.NoError(t, err)
				require.Len(t, awaits, 1)
				assert.Equal(t, "first", awaits[0].ID)
				awaits[0].Items[0].Payload[0] = '['
				untouched, err := eng.(engine.ToolAwaitQuerier).QueryToolAwaits(t.Context(), "root")
				require.NoError(t, err)
				assert.JSONEq(t, `{"limit":1}`, string(untouched[0].Items[0].Payload))
				require.NoError(t, rt.ProvideToolResults(t.Context(), rb7Answer("first")))
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					pending, queryErr := eng.(engine.ToolAwaitQuerier).QueryToolAwaits(t.Context(), "root")
					assert.NoError(c, queryErr)
					if assert.Len(c, pending, 1) {
						assert.Equal(c, "second", pending[0].ID)
					}
				}, time.Second, time.Millisecond)
				return rt.ProvideToolResults(t.Context(), rb7Answer("second"))
			})
		})
	}
}

func rb7Await(kind, id string) planner.AwaitItem {
	if kind == "questions" {
		return planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: id, ToolName: "test.answer", ToolCallID: id, Payload: rawjson.Message(`{"limit":1}`)})
	}
	return planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: id, Items: []planner.AwaitToolItem{{Name: "test.answer", ToolCallID: id, Payload: rawjson.Message(`{"limit":1}`)}}})
}

func rb7Answer(id string) *api.ToolResultsSet {
	return &api.ToolResultsSet{RunID: "root", ID: id, Results: []*api.ProvidedToolResult{{Name: "test.answer", ToolCallID: id, Result: rawjson.Message(`{"answer":"yes"}`)}}}
}
