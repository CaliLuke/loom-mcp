package runtime_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clarificationViewPlanner struct {
	awaitResumePlanner
	awaitOnly bool
}

// TestClarificationView covers A1-A4 for every shared clarification entry path.
func TestClarificationView(t *testing.T) {
	for _, path := range []string{"clarification", "pause", "missing_fields", "await_only", "before_questions", "after_questions", "before_external", "after_external"} {
		t.Run(path, func(t *testing.T) {
			plain := runClarificationView(t, path, false)
			compacted := runClarificationView(t, path, true)
			assert.Equal(t, clarificationExchange(plain), clarificationExchange(compacted), "A3: the retained exchange has identical semantics")
		})
	}
}

// TestAwaitAnswerViewControls checks A5 paths with explicit tool handshakes or
// typed answers outside model messages; these do not append clarification text.
func TestAwaitAnswerViewControls(t *testing.T) {
	for _, path := range []string{"questions", "typed", "external"} {
		t.Run(path, func(t *testing.T) {
			plain := runClarificationView(t, path, false)
			compacted := runClarificationView(t, path, true)
			assert.Equal(t, clarificationExchange(plain), clarificationExchange(compacted))
		})
	}
}

func (p clarificationViewPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	result, err := p.awaitResumePlanner.PlanStart(ctx, in)
	if err == nil && p.awaitOnly {
		result.FinalResponse = nil
	}
	return result, err
}

func runClarificationView(t *testing.T, path string, compacted bool) []*model.Message {
	t.Helper()
	var messages []*model.Message
	t.Run(fmt.Sprintf("compacted=%t", compacted), func(t *testing.T) {
		first := agenttest.Respond(agenttest.Text("checking"), agenttest.RawToolCall("resume.first", rawjson.Message(`{}`)), agenttest.CallID("first"))
		if path == "await_only" {
			first = agenttest.Respond(agenttest.Text("which?"))
		}
		script, err := agenttest.NewScript(first, agenttest.Respond(agenttest.Text("finished")))
		require.NoError(t, err)
		script.WithContextLimits(model.ContextLimits{ContextWindow: 6000})
		opts := []agenttest.Option{agenttest.PassthroughAll(), agenttest.WithResponder(clarificationViewResponders(path))}
		if compacted {
			opts = append(opts, agenttest.WithRuntimeOptions(runtime.WithContextCompaction("resume", runtime.ContextCompaction{KeepRecentTokens: 1000, MinSavingTokens: 20})))
		}
		h := agenttest.New(t, registerClarificationView(path, script.Client()), opts...)
		result, err := h.Run(t.Context(), "resume", clarificationViewSeed())
		require.NoError(t, err)
		require.NoError(t, result.RunErr)
		requests := script.Requests()
		require.Len(t, requests, 2)
		messages = requests[1].Messages
		assert.NoError(t, transcript.ValidateView(messages, transcript.GrammarBedrock{}))
		assert.NoError(t, transcript.ValidateView(messages, transcript.GrammarAnthropic{}))
		assert.Equal(t, compacted, hasClarificationCheckpoint(messages), "A1/A2: exercise an actual checkpoint")
		if path == "clarification" || path == "pause" || path == "missing_fields" || path == "await_only" || strings.Contains(path, "_") {
			assertClarificationExchange(t, messages, path)
		}
		assertClarificationRecord(t, result, requests)
	})
	return messages
}

func registerClarificationView(path string, client model.Client) agenttest.Register {
	return func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("script", client); err != nil {
			return err
		}
		specs := []tools.ToolSpec{
			{Name: "resume.first", Toolset: "resume", Payload: tools.TypeSpec{Name: "payload", Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Name: "result", Codec: tools.AnyJSONCodec}},
			{Name: "resume.external", Toolset: "resume", Payload: tools.TypeSpec{Name: "payload", Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Name: "result", Codec: tools.AnyJSONCodec}},
		}
		if err := rt.RegisterToolset(runtime.ToolsetRegistration{Name: "resume", Specs: specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			result := runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"done": true}})
			if path == "pause" {
				result.Pause = &runtime.ToolPause{Clarification: &runtime.ToolPauseClarification{ID: "pause", Question: "which?"}}
			}
			if path == "missing_fields" {
				result.ToolResult.Result = nil
				result.ToolResult.Error = planner.NewToolError("missing value")
				result.ToolResult.RetryHint = &planner.RetryHint{Reason: planner.RetryReasonMissingFields, MissingFields: []string{"value"}, ClarifyingQuestion: "which?"}
			}
			return result, nil
		}}); err != nil {
			return err
		}
		policy := runtime.RunPolicy{}
		if path == "missing_fields" {
			policy.OnMissingFields = runtime.MissingFieldsAwaitClarification
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "resume", Planner: clarificationViewPlanner{awaitResumePlanner: awaitResumePlanner{await: clarificationViewPath(path).await, specs: specs}, awaitOnly: path == "await_only"}, Specs: specs, Policy: policy,
			Workflow: engine.WorkflowDefinition{Name: "resume.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "resume.plan", ResumeActivityName: "resume.resume", ExecuteToolActivity: "resume.tool"})
	}
}

func clarificationViewResponders(path string) agenttest.Responder {
	if strings.HasPrefix(path, "before_") || strings.HasPrefix(path, "after_") {
		kind := strings.SplitN(path, "_", 2)[1]
		return agenttest.Responder{agenttest.Clarify("yes"), clarificationViewPath(kind).responder}
	}
	return agenttest.Responder{clarificationViewPath(path).responder}
}

func clarificationViewPath(path string) awaitResumePath {
	if strings.HasPrefix(path, "before_") || strings.HasPrefix(path, "after_") {
		kind := strings.SplitN(path, "_", 2)[1]
		clarification := clarificationViewPath("clarification").await.Items[0]
		external := clarificationViewPath(kind).await.Items[0]
		items := []planner.AwaitItem{clarification, external}
		if strings.HasPrefix(path, "after_") {
			items = []planner.AwaitItem{external, clarification}
		}
		return awaitResumePath{await: planner.NewAwait(items...)}
	}
	if path == "missing_fields" {
		return awaitResumePath{responder: agenttest.Clarify("yes")}
	}
	if path == "await_only" {
		path = "clarification"
	}
	for _, candidate := range awaitResumePaths() {
		if candidate.name == path {
			return candidate
		}
	}
	panic("unknown clarification test path: " + path)
}

func clarificationViewSeed() []*model.Message {
	var seed []*model.Message
	for i := 0; i < 30; i++ {
		seed = append(seed, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("seed ", 200)}}}, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "earlier"}}})
	}
	return append(seed, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "start"}}})
}

func hasClarificationCheckpoint(messages []*model.Message) bool {
	for _, message := range messages {
		if message.Meta[transcript.CheckpointMetaKey] == true {
			return true
		}
	}
	return false
}

// clarificationExchange drops the seed/checkpoint prefix while preserving the
// tool result, prompt and answer sequence (including missing-field reminders).
func clarificationExchange(messages []*model.Message) []*model.Message {
	for i, message := range messages {
		for _, part := range message.Parts {
			if use, ok := part.(model.ToolUsePart); ok && use.ID == "first" {
				return messages[i:]
			}
			if text, ok := part.(model.TextPart); ok && text.Text == "which?" {
				return messages[i:]
			}
		}
	}
	return nil
}

func assertClarificationExchange(t *testing.T, messages []*model.Message, path string) {
	t.Helper()
	promptAt := -1
	for i, message := range messages {
		if len(message.Parts) == 1 && message.Role == model.ConversationRoleAssistant {
			if text, ok := message.Parts[0].(model.TextPart); ok && text.Text == "which?" {
				promptAt = i
			}
		}
	}
	require.NotEqual(t, -1, promptAt, "record the actual clarification prompt")
	require.Less(t, promptAt+1, len(messages))
	assert.Equal(t, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "yes"}}}, messages[promptAt+1])
	if path != "await_only" {
		found := false
		for _, message := range messages[:promptAt] {
			for _, part := range message.Parts {
				if result, ok := part.(model.ToolResultPart); ok && result.ToolUseID == "first" {
					found = true
				}
			}
		}
		assert.True(t, found, "tool result must precede the clarification exchange")
	}
}

func assertClarificationRecord(t *testing.T, result *agenttest.Result, requests []*model.Request) {
	t.Helper()
	require.Len(t, result.Record.Runs, 1)
	run := result.Record.Runs[0]
	require.Len(t, run.Calls, len(requests))
	for i, call := range run.Calls {
		assert.Equal(t, requests[i].Messages, call.Request.Messages, "A4: exact RunRecord request reconstruction")
	}
	exported, err := result.Record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(exported)
	require.NoError(t, err)
	rebuilt, err := trajectory.Build(restored, result.RunID, trajectory.Options{})
	require.NoError(t, err)
	assert.Equal(t, result.Trajectory, rebuilt, "A4: trajectory survives a self-contained record round trip")
}
