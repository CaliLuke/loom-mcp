//nolint:lll // allow long lines in test literals for readability
package runtime

import (
	"context"
	"encoding/json/jsontext"
	"reflect"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/require"
)

func TestRunPlanActivityUsesOptions(t *testing.T) {
	rt := &Runtime{
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
		Bus:     noopHooks{},
	}
	wf := &testWorkflowContext{
		ctx:           context.Background(),
		hasPlanResult: true,
		planResult: &planner.PlanResult{
			FinalResponse: &planner.FinalResponse{
				Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}},
			},
		},
	}

	opts := engine.ActivityOptions{
		Queue:               "custom_queue",
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         engine.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoefficient: 2},
	}
	_, err := rt.runPlanActivity(wf, "calc.agent.plan", opts, PlanActivityInput{}, time.Time{})
	require.NoError(t, err)
	require.Equal(t, opts.Queue, wf.lastPlannerCall.Options.Queue)
	require.Equal(t, opts.StartToCloseTimeout, wf.lastPlannerCall.Options.StartToCloseTimeout)
	require.Equal(t, opts.RetryPolicy, wf.lastPlannerCall.Options.RetryPolicy)
}

func TestRunPlanActivityAcceptsTerminalFinalToolResult(t *testing.T) {
	rt := &Runtime{
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
		Bus:     noopHooks{},
	}
	wf := &testWorkflowContext{
		ctx:           context.Background(),
		hasPlanResult: true,
		planResult: &planner.PlanResult{
			FinalToolResult: &planner.FinalToolResult{
				Result: rawjson.Message([]byte(`{"status":"ok"}`)),
			},
		},
	}

	out, err := rt.runPlanActivity(wf, "calc.agent.plan", engine.ActivityOptions{}, PlanActivityInput{}, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.Result)
	require.NotNil(t, out.Result.FinalToolResult)
	require.JSONEq(t, `{"status":"ok"}`, string(out.Result.FinalToolResult.Result))
}

func TestRunPlanActivityRejectsNilPlanResultWithoutCriticalPrefix(t *testing.T) {
	rt := &Runtime{
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
		Bus:     noopHooks{},
	}
	wf := &testWorkflowContext{
		ctx: context.Background(),
	}

	_, err := rt.runPlanActivity(wf, "calc.agent.plan", engine.ActivityOptions{}, PlanActivityInput{}, time.Time{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil PlanResult")
	require.NotContains(t, err.Error(), "CRITICAL:")
}

func TestPlanStartActivityInvokesPlanner(t *testing.T) {
	called := false
	pl := &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
		called = true
		require.NotNil(t, input)
		require.Equal(t, run.Context{RootRunID: "test-root", RunID: "run-123"}, input.RunContext)
		require.Len(t, input.Messages, 1)
		require.Equal(t, "hello", agentMessageText(input.Messages[0]))
		require.NotNil(t, input.Agent)
		return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}}, nil
	}}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	input := PlanActivityInput{AgentID: "service.agent", RunID: "run-123", View: testSeedView(t, rt, []*model.Message{{Role: "user", Parts: []model.Part{model.TextPart{Text: "hello"}}}}, "test-root"), RunContext: run.Context{RootRunID: "test-root", RunID: "run-123"}}
	out, err := rt.PlanStartActivity(context.Background(), &input)
	require.NoError(t, err)
	require.True(t, called)
	require.NotNil(t, out.Result.FinalResponse)
}

func TestPlannerBoundaryOmitsToolResultsField(t *testing.T) {
	t.Parallel()

	planResumeInputType := reflect.TypeOf(planner.PlanResumeInput{})
	_, hasPlannerToolResults := planResumeInputType.FieldByName("ToolResults")
	require.False(t, hasPlannerToolResults, "PlanResumeInput must expose ToolOutputs as its only execution-history field")

	planActivityInputType := reflect.TypeOf(PlanActivityInput{})
	_, hasActivityToolResults := planActivityInputType.FieldByName("ToolResults")
	require.False(t, hasActivityToolResults, "PlanActivityInput must expose ToolOutputs as its only execution-history field")
}

func TestPlanResumeActivityPassesToolOutputs(t *testing.T) {
	called := false
	toolOutputs := []*api.ToolCallOutput{{
		Name:       "svc.ts.tool",
		ToolCallID: "call-1",
		Payload:    rawjson.Message([]byte(`{"from":"test"}`)),
		Result:     rawjson.Message([]byte(`{"status":"ok"}`)),
		ServerData: rawjson.Message([]byte(`[{"kind":"evidence"}]`)),
	}}
	pl := &stubPlanner{resume: func(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
		called = true
		require.NotNil(t, input)
		require.Len(t, input.ToolOutputs, 1)
		require.Equal(t, tools.Ident("svc.ts.tool"), input.ToolOutputs[0].Name)
		require.Equal(t, "call-1", input.ToolOutputs[0].ToolCallID)
		require.JSONEq(t, `{"from":"test"}`, string(testLoadToolPayload(t, ctx, input)))
		require.JSONEq(t, `{"status":"ok"}`, string(testLoadToolResult(t, ctx, input)))
		require.JSONEq(t, `[{"kind":"evidence"}]`, string(testLoadServerData(t, ctx, input)))
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "svc.other.tool"}}}, nil
	}}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	input := PlanActivityInput{
		AgentID:    "service.agent",
		RunID:      "run-123",
		RunContext: run.Context{RootRunID: "root", RunID: "run-123", Attempt: 3},
		View:       testOutputView(t, rt, toolOutputs),
	}
	out, err := rt.PlanResumeActivity(context.Background(), &input)
	require.NoError(t, err)
	require.True(t, called)
	require.Len(t, out.Result.ToolCalls, 1)
}

func TestBuildPlannerToolOutputsPreservesOmittedResultMetadata(t *testing.T) {
	t.Parallel()

	rt := &Runtime{
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			"svc.ts.tool": newAnyJSONSpec("svc.ts.tool", "svc.tools"),
		},
		logger:  telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{},
		tracer:  telemetry.NoopTracer{},
	}

	outputs, err := rt.buildPlannerToolOutputs(
		context.Background(),
		[]planner.ToolRequest{
			{
				Name:       "svc.ts.tool",
				ToolCallID: "call-1",
				Payload:    rawjson.Message([]byte(`{"from":"test"}`)),
			},
		},
		[]*planner.ToolResult{
			{
				Name:                "svc.ts.tool",
				ToolCallID:          "call-1",
				ResultOmitted:       true,
				ResultOmittedReason: "workflow_budget",
				ResultBytes:         12345,
				ServerData:          rawjson.Message([]byte(`[{"kind":"evidence"}]`)),
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.True(t, outputs[0].ResultOmitted)
	require.Equal(t, "workflow_budget", outputs[0].ResultOmittedReason)
	require.Equal(t, 12345, outputs[0].ResultBytes)
	require.Empty(t, outputs[0].Result)
	require.JSONEq(t, `[{"kind":"evidence"}]`, string(outputs[0].ServerData))
}

func TestPlannerViewRejectsMissingOutputMetadata(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{}
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.PolicyDenial, ProtocolVersion: index.TemplateProtocolVersion}, Output: "sha256:missing"}))
	_, err := rt.loadView(t.Context(), "root", "run", base.view())
	require.ErrorIs(t, err, runstore.ErrNotFound)
}

func TestPlanResumeActivityPreservesEmptyRawJSONPayloads(t *testing.T) {
	pl := &stubPlanner{
		resume: func(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
			return &planner.PlanResult{
				ToolCalls: []planner.ToolRequest{
					{
						Name:    "svc.other.tool",
						Payload: rawjson.Message([]byte{}),
					},
				},
				Await: planner.NewAwait(
					planner.AwaitQuestionsItem(&planner.AwaitQuestions{
						ID:         "await-q",
						ToolName:   "chat.ask_question.ask_question",
						ToolCallID: "call-q",
						Payload:    rawjson.Message([]byte{}),
					}),
					planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{
						ID: "await-ext",
						Items: []planner.AwaitToolItem{
							{
								Name:       "external.one",
								ToolCallID: "call-ext",
								Payload:    rawjson.Message([]byte{}),
							},
						},
					}),
				),
			}, nil
		},
	}
	rt := newTestRuntimeWithPlanner("service.agent", pl)
	rt.toolSpecs["svc.other.tool"] = tools.ToolSpec{Name: "svc.other.tool"}
	input := PlanActivityInput{
		AgentID:    "service.agent",
		RunID:      "run-123",
		RunContext: run.Context{RootRunID: "root", RunID: "run-123"},
	}
	out, err := rt.PlanResumeActivity(context.Background(), &input)
	require.NoError(t, err)
	require.Len(t, out.Result.ToolCalls, 1)
	require.NotNil(t, out.Result.ToolCalls[0].Payload)
	require.Empty(t, out.Result.ToolCalls[0].Payload)
	require.NotNil(t, out.Result.Await)
	require.Len(t, out.Result.Await.Items, 2)
	require.NotNil(t, out.Result.Await.Items[0].Questions)
	require.NotNil(t, out.Result.Await.Items[0].Questions.Payload)
	require.Empty(t, out.Result.Await.Items[0].Questions.Payload)
	require.NotNil(t, out.Result.Await.Items[1].ExternalTools)
	require.Len(t, out.Result.Await.Items[1].ExternalTools.Items, 1)
	require.NotNil(t, out.Result.Await.Items[1].ExternalTools.Items[0].Payload)
	require.Empty(t, out.Result.Await.Items[1].ExternalTools.Items[0].Payload)
}

func TestNormalizeTranscriptRawJSONNormalizesEmptyRawMessageValues(t *testing.T) {
	messages := []*model.Message{
		{
			Role: "assistant",
			Parts: []model.Part{
				model.ToolUsePart{
					ID:    "call-1",
					Name:  "tool.one",
					Input: jsontext.Value{},
				},
				model.ToolResultPart{
					ToolUseID: "call-1",
					Content: map[string]any{
						"payload": jsontext.Value{},
					},
				},
			},
			Meta: map[string]any{
				"raw": jsontext.Value{},
			},
		},
	}

	normalizeTranscriptRawJSON(messages)

	toolUse, ok := messages[0].Parts[0].(model.ToolUsePart)
	require.True(t, ok)
	require.Nil(t, toolUse.Input)
	toolResult, ok := messages[0].Parts[1].(model.ToolResultPart)
	require.True(t, ok)
	content, ok := toolResult.Content.(map[string]any)
	require.True(t, ok)
	require.Nil(t, content["payload"])
	require.Nil(t, messages[0].Meta["raw"])
}
