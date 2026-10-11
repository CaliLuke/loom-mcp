package runtime

import (
	"context"
	"encoding/json/v2"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvideToolResultsRejectsInvalidSubmissionWithoutChangingRun(t *testing.T) {
	for _, kind := range []string{"external", "questions"} {
		for _, defect := range []string{"unknown call", "wrong name", "unregistered tool", "contract violation", "decode failure", "wrong await", "missing await"} {
			t.Run(kind+"/"+defect, func(t *testing.T) {
				eng := engineinmem.New()
				store := &contentPathStore{Store: storeinmem.New()}
				bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
				rt := New(WithEngine(eng), WithRunStore(store), WithHooks(bus))
				spec := providedValidationSpec()
				var materializations atomic.Int32
				expectedPreflightCalls := int32(0)
				if defect == "contract violation" {
					expectedPreflightCalls = 1
				}
				require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, ResultMaterializer: func(context.Context, ToolCallMeta, *planner.ToolRequest, *planner.ToolResult) error {
					materializations.Add(1)
					return nil
				}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
					panic("external tool must not execute")
				}}))
				client := rt
				if defect == "unregistered tool" {
					client = New(WithEngine(eng), WithRunStore(store))
				}
				item := planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "await", Items: []planner.AwaitToolItem{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}})
				if kind == "questions" {
					item = planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: "await", ToolName: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)})
				}
				registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
					return &planner.PlanResult{Await: planner.NewAwait(item)}, nil
				}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
					assert.Equal(t, expectedPreflightCalls+2, materializations.Load(), "valid results materialize in preflight and activity")
					awaits, err := eng.(engine.ToolAwaitQuerier).QueryToolAwaits(ctx, "root")
					require.NoError(t, err)
					assert.Empty(t, awaits, "consumed awaits must no longer accept results")
					require.Len(t, in.ToolOutputs, 1)
					data, err := in.Content.LoadToolResult(ctx, in.ToolOutputs[0].Refs)
					require.NoError(t, err)
					assert.JSONEq(t, `{"answer":"yes"}`, string(data))
					return oracleFinal(), nil
				}})
				runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
					if event.Type() != hooks.RunPaused {
						return nil
					}
					valid := &api.ProvidedToolResult{Name: spec.Name, ToolCallID: "call", Result: rawjson.Message(`{"answer":"yes"}`)}
					invalid := *valid
					submission := &api.ToolResultsSet{RunID: "root", ID: "await", Results: []*api.ProvidedToolResult{&invalid}}
					switch defect {
					case "unknown call":
						invalid.ToolCallID = "unknown"
					case "wrong name":
						invalid.Name = "test.other"
					case "unregistered tool":
					case "contract violation":
						invalid.Bounds = &agent.Bounds{}
					case "decode failure":
						invalid.Result = rawjson.Message(`{"answer":42}`)
					case "wrong await":
						submission.ID = "other"
					case "missing await":
						submission.ID = ""
					}
					before := store.snapshot()
					err := client.ProvideToolResults(t.Context(), submission)
					assertToolResultContractError(t, err)
					assert.Equal(t, expectedPreflightCalls, materializations.Load(), "identity and decode failures precede materialization")
					assert.Equal(t, before, store.snapshot(), "invalid input must not write content")
					snapshot, err := rt.GetRunSnapshot(t.Context(), "root")
					require.NoError(t, err)
					assert.Equal(t, run.StatusPaused, snapshot.Status)
					require.NotNil(t, snapshot.Await)
					assert.Equal(t, "await", snapshot.Await.ID)
					return rt.ProvideToolResults(t.Context(), &api.ToolResultsSet{RunID: "root", ID: "await", Results: []*api.ProvidedToolResult{valid}})
				})
			})
		}
	}
}

func TestProvidedToolResultsDefenseErrorsAreNonRetryable(t *testing.T) {
	for _, defect := range []string{"missing result", "wrong name", "unregistered tool", "decode failure"} {
		t.Run(defect, func(t *testing.T) {
			rt := New()
			spec := providedValidationSpec()
			if defect != "unregistered tool" {
				require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
					panic("external tool must not execute")
				}}))
			}
			call := planner.ToolRequest{Name: spec.Name, ToolCallID: "call"}
			item := &api.ProvidedToolResult{Name: spec.Name, ToolCallID: "call", Result: rawjson.Message(`{"answer":"yes"}`)}
			switch defect {
			case "missing result":
				item = nil
			case "wrong name":
				item.Name = "test.other"
			case "unregistered tool":
			case "decode failure":
				item.Result = rawjson.Message(`{"answer":42}`)
			}
			_, _, err := rt.decodeProvidedToolResults(t.Context(), []planner.ToolRequest{call}, map[string]*api.ProvidedToolResult{"call": item})
			assertToolResultContractError(t, err)
		})
	}
}

func providedValidationSpec() tools.ToolSpec {
	spec := newAnyJSONSpec("test.answer", "test")
	spec.Result.Codec.FromJSON = func(data []byte) (any, error) {
		var result struct {
			Answer string `json:"answer"`
		}
		err := json.Unmarshal(data, &result)
		return result, err
	}
	return spec
}

func TestProvidedToolResultsIndexErrorsAreNonRetryable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []*api.ProvidedToolResult
	}{
		{name: "empty"},
		{name: "nil", results: []*api.ProvidedToolResult{nil}},
		{name: "missing call", results: []*api.ProvidedToolResult{{Name: "test.answer"}}},
		{name: "unknown call", results: []*api.ProvidedToolResult{{ToolCallID: "unknown"}}},
		{name: "duplicate call", results: []*api.ProvidedToolResult{{ToolCallID: "call"}, {ToolCallID: "call"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := indexProvidedToolResults(&api.ToolResultsSet{Results: tc.results}, map[string]struct{}{"call": {}})
			assertToolResultContractError(t, err)
		})
	}
}
