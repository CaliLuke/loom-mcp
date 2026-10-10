package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeIndexOracleBookkeeping(t *testing.T) {
	for _, mode := range []string{"mixed", "emptied_assistant_removed", "text_retained"} {
		t.Run(mode, func(t *testing.T) {
			bus := &recordingHooks{ch: make(chan hooks.Event, 128)}
			rt := New(WithEngine(engineinmem.New()), WithHooks(bus))
			specs := []tools.ToolSpec{bookkeepingSpec("control.progress", false), newAnyJSONSpec("test.lookup", "control")}
			specs[0].Toolset = "control"
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "control", Specs: specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				result := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: true}
				if call.ToolCallID == "visible" {
					result.Result = "ok"
				}
				if call.ToolCallID == "failed" {
					result.Result = nil
					result.Error = planner.NewToolError("failed")
				}
				return Executed(result), nil
			}}))
			var expected []*model.Message
			if mode == "mixed" {
				expected = frozenIndexMessages(t, "TestIndexOracleMixedBookkeepingBatch/5/mixed batch retains domain and failed bookkeeping results")
			} else {
				expected = frozenIndexMessages(t, "TestIndexOracleBookkeepingHiding/"+mode+"/6/bookkeeping-only batch skips user message and reminder")
			}
			resumed := 0
			registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				if mode == "text_retained" {
					in.Events.AssistantChunk(ctx, "keep this")
				}
				result := &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "control.progress", ToolCallID: "hidden"}}}
				if mode == "mixed" {
					result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: "test.lookup", ToolCallID: "visible"}, planner.ToolRequest{Name: "control.progress", ToolCallID: "failed"})
				} else {
					result.Await = planner.NewAwait(planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "typed", Schema: rawjson.Message(`{"type":"object"}`)}))
				}
				return result, nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				resumed++
				assertIndexMessages(t, expected, in.Messages)
				if mode == "mixed" {
					assert.Len(t, in.ToolOutputs, 2)
				} else {
					assert.Empty(t, in.ToolOutputs)
				}
				return oracleFinal(), nil
			}})
			runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
				if e, ok := event.(*hooks.AwaitTypedInputEvent); ok {
					return rt.ProvideTypedInput(t.Context(), &api.TypedInputAnswer{RunID: "root", ID: e.ID, Payload: rawjson.Message(`{}`)})
				}
				return nil
			})
			assert.Positive(t, resumed)
		})
	}
}

func TestRuntimeIndexOracleBatchRemindersAndMissingFields(t *testing.T) {
	for _, clarify := range []bool{false, true} {
		t.Run(fmt.Sprint(clarify), func(t *testing.T) {
			bus := &recordingHooks{ch: make(chan hooks.Event, 128)}
			rt := New(WithEngine(engineinmem.New()), WithHooks(bus))
			names := []tools.Ident{"test.small", "test.large", "test.failed"}
			specs := make([]tools.ToolSpec, len(names))
			for i, name := range names {
				specs[i] = newAnyJSONSpec(name, "test")
				specs[i].ResultReminder = "Remember " + string(name)
				if name == "test.large" {
					specs[i].Bounds = &tools.BoundsSpec{}
				}
			}
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				result := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID}
				switch string(call.Name) {
				case "test.small":
					result.Result = map[string]any{"count": float64(7)}
				case "test.large":
					result.Result = map[string]string{"data": strings.Repeat("x", 100_000)}
					result.Bounds = &agent.Bounds{Returned: 1, Truncated: true, RefinementHint: "narrow the query"}
				case "test.failed":
					result.Error = planner.NewToolError("bad request")
					result.RetryHint = &planner.RetryHint{Reason: planner.RetryReasonMissingFields, MissingFields: []string{"id"}, ClarifyingQuestion: "Which ID?"}
				}
				return Executed(result), nil
			}}))
			expected := frozenIndexMessages(t, "TestIndexOracleResultBatchAndReminders/5/appendUserToolResults order, missing IDs, omission, retry/bounds reminders")
			// The old helper oracle also exercised a missing result. A real dispatch
			// always supplies a result, so omit that unmatched call from this script.
			expected[0].Parts = expected[0].Parts[:3]
			// Actual tool activities have always enriched missing-field hints with
			// the generated payload field contract, before the message helper runs.
			reminder := expected[2].Parts[0].(model.TextPart)
			reminder.Text = strings.ReplaceAll(reminder.Text, "Reason: missing_fields\n", "Reason: missing_fields\nMessage: Field contract: object\n")
			expected[2].Parts[0] = reminder
			if clarify {
				expected = append(expected, contentMessage("missing field answer"))
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: names[0], ToolCallID: "small"}, {Name: names[1], ToolCallID: "large"}, {Name: names[2], ToolCallID: "failed"}}}, nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				assertIndexMessages(t, expected, in.Messages)
				return oracleFinal(), nil
			}})
			if clarify {
				reg := rt.agents["content.agent"]
				reg.Policy.OnMissingFields = MissingFieldsAwaitClarification
				rt.agents[reg.ID] = reg
			}
			runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
				if e, ok := event.(*hooks.AwaitClarificationEvent); ok {
					return rt.ProvideClarification(t.Context(), &api.ClarificationAnswer{RunID: "root", ID: e.ID, Answer: "missing field answer"})
				}
				return nil
			})
		})
	}
}
