package trajectory_test

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildDecodesWholeLargeResult(t *testing.T) {
	f := newFixture()
	body := strings.Repeat("x", 5_000_000)
	call := f.put(t, planner.ToolRequest{Name: "tool.read", ToolCallID: "large", Payload: rawjson.Message(`{}`)})
	result := f.put(t, map[string]any{"body": body, "end": true})
	preview := f.put(t, map[string]any{"body": "bounded preview", "end": false})
	bounds := &agent.Bounds{Returned: 1}
	output := f.put(t, planner.ToolOutput{Name: "tool.read", ToolCallID: "large", Refs: tools.ResultRefs{Result: result}, Bounds: bounds})
	head := f.segment(t, "", []index.MessageEntry{
		{ID: 1, Kind: index.Assistant, Calls: []runstore.Hash{call}},
		{ID: 2, Kind: index.ToolResult, Hash: preview, Output: output, ToolUseIDs: []string{"large"}, Batch: 2, Result: &index.ResultProjection{Preview: index.TextParameter{Text: "bounded preview"}}},
	})
	f.event(t, hooks.NewRunSealedEvent("run", head, api.WorkflowProtocolVersion, time.Unix(1, 0)), 1)
	rec, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	got, err := trajectory.Build(rec, "run", trajectory.Options{})
	require.NoError(t, err)
	steps := recordSteps(got)
	require.Len(t, steps, 1)
	var decoded struct {
		Body string `json:"body"`
		End  bool   `json:"end"`
	}
	require.NoError(t, json.Unmarshal(steps[0].Tool.Result, &decoded))
	assert.Equal(t, body, decoded.Body)
	assert.True(t, decoded.End)
	assert.Equal(t, bounds, steps[0].Tool.Bounds)
}

func TestBuildEveryAwaitAndAnswer(t *testing.T) {
	for _, test := range []struct {
		name       string
		event      hooks.Event
		answerKind index.EntryKind
		expected   trajectory.StepKind
		answerStep int
	}{
		{"clarification", hooks.NewAwaitClarificationEvent("run", "agent", "session", "id", "clarify", nil, "", nil), index.Clarification, trajectory.StepAnswer, 2},
		{"questions", hooks.NewAwaitQuestionsEvent("run", "agent", "session", "id", "tool.ask", "call", rawjson.Message(`{}`), nil, []hooks.AwaitQuestion{{ID: "q", Prompt: "choose"}}), index.ToolResult, trajectory.StepTool, 1},
		{"typed input", hooks.NewAwaitTypedInputEvent("run", "agent", "session", "id", "typed", rawjson.Message(`{"type":"object"}`)), index.TypedInput, trajectory.StepAnswer, 2},
		{"confirmation", hooks.NewAwaitConfirmationEvent("run", "agent", "session", "id", "confirm", "approve", "tool.ask", "call", rawjson.Message(`{}`)), "", trajectory.StepAnswer, 1},
		{"external tools", hooks.NewAwaitExternalToolsEvent("run", "agent", "session", "id", []hooks.AwaitToolItem{{ToolName: "tool.ask", ToolCallID: "call", Payload: rawjson.Message(`{}`)}}), index.ToolResult, trajectory.StepTool, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture()
			first := f.call(t, 1, 1, runstore.CallCompleted, &model.Response{}, "", "")
			f.event(t, hooks.NewPlannerStepEvent("run", "agent", "session", 1, index.ViewRef{}, []runstore.CallKey{first}, api.WorkflowProtocolVersion, time.Unix(0, 0)), 0)
			f.event(t, test.event, 1)
			head := awaitAnswerHead(t, f, test.answerKind, first)
			second := f.call(t, 2, 1, runstore.CallCompleted, &model.Response{}, "", head)
			f.event(t, hooks.NewPlannerStepEvent("run", "agent", "session", 2, index.ViewRef{IndexHead: head}, []runstore.CallKey{second}, api.WorkflowProtocolVersion, time.Unix(3, 0)), 3)
			f.event(t, hooks.NewRunSealedEvent("run", head, api.WorkflowProtocolVersion, time.Unix(4, 0)), 4)
			rec, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			require.NoError(t, err)
			got, err := trajectory.Build(rec, "run", trajectory.Options{EventsUnverified: true})
			require.NoError(t, err)
			require.Len(t, got.Steps, 4)
			var await, answer trajectory.Step
			for _, step := range got.Steps {
				if step.Kind == trajectory.StepAwait {
					await = step
				} else if step.Model == nil {
					answer = step
				}
			}
			require.NotNil(t, await.Event)
			assert.Equal(t, test.event.Type(), await.Event.Type)
			assert.Equal(t, 1, await.Step)
			assert.Equal(t, test.answerStep, answer.Step)
			assert.False(t, await.Verified)
			assert.Equal(t, test.expected, answer.Kind)
			switch test.answerKind {
			case index.Clarification:
				require.NotNil(t, answer.Answer)
				assert.Equal(t, `"complete clarification"`, string(answer.Answer.Content))
				assert.Equal(t, index.Clarification, answer.Answer.Kind)
				assert.True(t, answer.Verified)
			case index.TypedInput:
				require.NotNil(t, answer.Answer)
				assert.Equal(t, `{"large":9007199254740993}`, string(answer.Answer.Content))
				assert.Equal(t, "id", answer.Answer.InputID)
				assert.True(t, answer.Verified)
			case index.ToolResult:
				require.NotNil(t, answer.Tool)
				assert.Equal(t, `{"large":9007199254740993}`, string(answer.Tool.Result))
				assert.True(t, answer.Verified)
			case index.Message, index.Assistant, index.Structural:
				t.Fatal("unexpected answer kind")
			default:
				require.NotNil(t, answer.Event)
				assert.Equal(t, hooks.ToolAuthorization, answer.Event.Type)
				assert.Contains(t, string(answer.Event.Payload), `"Approved":true`)
				assert.False(t, answer.Verified)
			}
			assertAwaitAnswerJSON(t, got, test.answerKind)
		})
	}
}

func awaitAnswerHead(t *testing.T, f fixture, kind index.EntryKind, producing runstore.CallKey) runstore.Hash {
	t.Helper()
	switch kind {
	case index.Clarification:
		return f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: kind, Hash: f.putBytes(t, []byte("complete clarification"))}})
	case index.TypedInput:
		return f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: kind, InputID: "id", Hash: f.putBytes(t, []byte(`{"large":9007199254740993}`))}})
	case index.ToolResult:
		call := f.put(t, planner.ToolRequest{Name: "tool.ask", ToolCallID: "call", Payload: rawjson.Message(`{}`)})
		result := f.putBytes(t, []byte(`{"large":9007199254740993}`))
		output := f.put(t, planner.ToolOutput{Name: "tool.ask", ToolCallID: "call", Refs: tools.ResultRefs{Result: result}})
		return f.segment(t, "", []index.MessageEntry{
			{ID: 1, Kind: index.Assistant, Calls: []runstore.Hash{call}, Call: &producing},
			{ID: 2, Kind: kind, Hash: result, Output: output, ToolUseIDs: []string{"call"}, Batch: 2},
		})
	case index.Message, index.Assistant, index.Structural:
		t.Fatal("unexpected answer kind")
		return ""
	default:
		f.event(t, hooks.NewToolAuthorizationEvent("run", "agent", "session", "tool.ask", "call", true, "approved", "operator"), 2)
		return ""
	}
}

// assertAwaitAnswerJSON checks the actual serialized answer surface for each await kind.
func assertAwaitAnswerJSON(t *testing.T, got *trajectory.Trajectory, kind index.EntryKind) {
	t.Helper()
	data, err := got.Marshal()
	require.NoError(t, err)
	switch kind {
	case index.Clarification:
		assert.Contains(t, string(data), `"content":"complete clarification"`)
	case index.TypedInput:
		assert.Contains(t, string(data), `"content":{"large":9007199254740993}`)
	case index.ToolResult:
		assert.Contains(t, string(data), `"result":{"large":9007199254740993}`)
	case index.Message, index.Assistant, index.Structural:
		t.Fatal("unexpected answer kind")
	default:
		assert.Contains(t, string(data), `"Approved":true`)
	}
}
