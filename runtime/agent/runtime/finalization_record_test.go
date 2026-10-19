package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"

	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRecordFinalizationResultsRemainOutsideTranscript(t *testing.T) {
	for _, fail := range []bool{true, false} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			eng := &contentBatchEngine{Engine: engineinmem.New()}
			rt := New(WithoutContextDefault("content.agent"), WithEngine(eng))
			domain := newAnyJSONSpec("test.domain", "test")
			terminal := bookkeepingSpec("test.control_limit", true)
			terminal.Toolset = "test"
			var terminalID string
			domainCalls := 0
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Inline: true, Specs: []tools.ToolSpec{domain, terminal}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				result := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: "read complete"}
				if call.Name == domain.Name {
					domainCalls++
				} else {
					terminalID = call.ToolCallID
					result.Result = "limit recorded"
					if fail {
						result.Result = nil
						result.Error = planner.NewToolError("write failed")
					}
				}
				return Executed(result), nil
			}}))
			var lastMessages []*model.Message
			next := func() *planner.PlanResult {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: domain.Name, Payload: rawjson.Message(`{}`)}}}
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return next(), nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				lastMessages = cloneMessages(in.Messages)
				return next(), nil
			}})
			reg := rt.agents["content.agent"]
			reg.Specs = []tools.ToolSpec{domain, terminal}
			rt.agents[reg.ID] = reg
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"), WithRunMaxToolCalls(1), WithLimitTerminalPlans(LimitTerminalPlans{
				TimeBudget:  LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{}`)},
				ToolCallCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{}`)},
				RecoveryCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{}`)},
			}))
			if fail {
				require.ErrorContains(t, err, "write failed")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, 1, domainCalls)
			require.NotEmpty(t, terminalID)
			require.Len(t, lastMessages, 2)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "root")
			require.NoError(t, err)
			require.Len(t, record.Runs, 1)
			run := record.Runs[0]
			assert.True(t, run.Sealed)
			assertIndexMessages(t, lastMessages, run.Transcript)
			uses := make(map[string]bool)
			for _, message := range run.Transcript {
				for _, part := range message.Parts {
					switch part := part.(type) {
					case model.ToolUsePart:
						uses[part.ID] = true
					case model.ToolResultPart:
						assert.True(t, uses[part.ToolUseID], "orphan tool_result %s", part.ToolUseID)
					}
				}
			}
			for _, entry := range run.Entries {
				assert.NotContains(t, entry.ToolUseIDs, terminalID, "an unindexed terminal call must have no result or hiding entry")
				for _, hash := range entry.Calls {
					var call planner.ToolRequest
					require.NoError(t, json.Unmarshal(testStoredBytes(t, rt, "root", hash), &call))
					assert.NotEqual(t, terminalID, call.ToolCallID)
				}
			}
			eng.mu.Lock()
			defer eng.mu.Unlock()
			contentCalls := 3
			if !fail {
				// Successful inline finalization already stores its body before K8.
				contentCalls++
			}
			require.Len(t, eng.records, contentCalls, "sealing must add no RecordContent activity")
			assert.NotNil(t, eng.records[0].ModelCallPolicy, "initial cap resolution is recorded even without seed messages")
			assert.Nil(t, eng.records[1].FinalizeCalls)
			assert.NotNil(t, eng.records[2].FinalizeCalls)
			if !fail {
				assert.Equal(t, [][]byte{[]byte(`"limit recorded"`)}, eng.records[3].Items, "terminal publication stores only its body, without index metadata")
			}
		})
	}
}

func TestStageResultRequiresIndexedCall(t *testing.T) {
	rt := New(WithoutContextDefault("content.agent"))
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	call := planner.ToolRequest{Name: "test.domain", ToolCallID: "indexed"}
	testRecordAssistant(t, rt, base, []planner.ToolRequest{call})
	base.stageUnindexedResult(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID})
	base.stageUnindexedResult(&planner.ToolResult{Name: "test.control_limit", ToolCallID: "unindexed"})
	assert.Contains(t, base.unindexedResults, call.ToolCallID)
	assert.NotContains(t, base.unindexedResults, "unindexed")
}
