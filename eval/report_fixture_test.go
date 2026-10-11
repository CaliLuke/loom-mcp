package eval

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reportPlanner struct{ specs []tools.ToolSpec }

const reportFixtureID = "0199d4a0-0000-7000-8000-000000000014"

var updateReports = flag.Bool("update", false, "regenerate report fixtures from scripted runs and update report goldens")

func loadReportFixture(t *testing.T) *Experiment {
	t.Helper()
	if *updateReports {
		writeReportFixture(t)
	}
	store := &Store{dir: "testdata/report/store"}
	e, err := store.Load(reportFixtureID)
	require.NoError(t, err)
	return e
}

func reportGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata/report", name+".golden")
	if *updateReports {
		require.NoError(t, os.WriteFile(path, []byte(got), 0600))
	}
	want, err := os.ReadFile(filepath.Join("testdata/report", filepath.Base(name)+".golden"))
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// writeReportFixture records real scripted multi-turn runs; all quality annotations
// are explicit stored evidence so the fixture does not depend on T17 judging.
func writeReportFixture(t *testing.T) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
			return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "report", Planner: reportPlanner{}, Workflow: engine.WorkflowDefinition{Name: "report.workflow", TaskQueue: "report", Handler: rt.ExecuteWorkflow}, PlanActivityName: "report.plan", ResumeActivityName: "report.resume", ExecuteToolActivity: "report.tool"})
		}, agenttest.ScriptedModel("report", agenttest.Respond(agenttest.Text("first answer"), agenttest.Usage(2, 1)), agenttest.Respond(agenttest.Text("second answer"), agenttest.Usage(3, 1))))
		session, err := h.RunSession(t.Context(), "report", agenttest.User("first"), agenttest.User("second"))
		require.NoError(t, err)
		require.Len(t, session.Runs, 2)
		a, err := storedAttempt(&AttemptRecord{AttemptKey: AttemptKey{Variant: "base", Scenario: "chat", Item: "failed", Repeat: 1}, Outcome: &Outcome{Session: session}})
		require.NoError(t, err)
		a.Verdict, a.Task, a.Reference, a.Duration, a.Tokens, a.JudgeTokens = VerdictFailed, "Answer accurately", "reference answer", 1500*time.Millisecond, 7, 4
		a.Checks = []expect.Check{{Name: "runs_succeeded", Passed: true}, {Name: "correct", Diagnostic: "answer differs"}}
		threshold := 0.8
		a.Scores = []expect.Score{{Name: "quality", Value: 0.5, Threshold: &threshold}}
		a.Claims = []StoredClaim{{ID: "truth", Text: "Answer is true"}, {ID: "style", Text: "Brief", Informational: true}, {ID: "judge_error", Text: "Grounded"}}
		a.Judgments = []StoredJudgment{{ClaimID: "truth", Label: "contradicted", Rationale: "wrong fact", Uncalibrated: true, Truncated: true}, {ClaimID: "style", Label: "entailed", Rationale: "brief enough", Informational: true}, {ClaimID: "judge_error", Error: "judge unavailable"}}
		a.Runs[0].TraceID = "0123456789abcdef0123456789abcdef"
		a.Started = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
		e := &Experiment{Header: ExperimentHeader{Format: experimentFormat, Version: 1, ID: reportFixtureID, Label: "report fixture", Status: statusComplete, Started: a.Started, Finished: a.Started.Add(time.Second), Source: VCS{Revision: "94d1f173", Modified: true}, Judge: &JudgeInfo{ModelID: "scripted-judge", PromptHash: "sha256:" + strings.Repeat("a", 64)}, Variants: []StoredVariant{{ID: "base"}}, Scenarios: []ScenarioInfo{{ID: "chat", Items: []string{"failed"}}}, Planned: 1}, Attempts: []*StoredAttempt{a}}
		for i, script := range [][]agenttest.Turn{
			{agenttest.Respond(agenttest.RawToolCall("report.lookup", rawjson.Message(`{}`))), agenttest.Respond(agenttest.RawToolCall("report.refund", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))},
			{agenttest.Respond(agenttest.RawToolCall("report.lookup", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))},
			{agenttest.Respond(agenttest.RawToolCall("delegate.child", rawjson.Message(`{}`))), agenttest.Respond(agenttest.RawToolCall("report.lookup", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("child done")), agenttest.Respond(agenttest.Text("done"))},
		} {
			toolHarness := agenttest.New(t, registerReportTools, agenttest.ScriptedModel("report", script...), agenttest.PassthroughAll())
			toolSession, runErr := toolHarness.RunSession(t.Context(), "report", agenttest.User("help"))
			require.NoError(t, runErr)
			require.NoError(t, toolSession.Runs[0].RunErr)
			attempt, storeErr := storedAttempt(&AttemptRecord{Index: i + 1, AttemptKey: AttemptKey{Variant: "base", Scenario: "chat", Item: fmt.Sprintf("tools-%d", i), Repeat: 1}, Outcome: &Outcome{Session: toolSession, Checks: []expect.Check{{Name: "ran", Passed: true}}}})
			require.NoError(t, storeErr)
			e.Attempts = append(e.Attempts, attempt)
			e.Header.Scenarios[0].Items = append(e.Header.Scenarios[0].Items, attempt.Item)
		}
		e.Header.Planned = len(e.Attempts)
		dir := filepath.Join("testdata/report/store", reportFixtureID)
		require.NoError(t, os.MkdirAll(dir, 0700))
		header, err := encodeStore(e.Header)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "experiment.json"), append(header, '\n'), 0600))
		prefix, err := encodeStore(attemptsHeader{Format: attemptsFormat, Version: 1, Experiment: reportFixtureID})
		require.NoError(t, err)
		lines := string(prefix) + "\n"
		for _, attempt := range e.Attempts {
			data, err := encodeStore(attempt)
			require.NoError(t, err)
			lines += string(data) + "\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "attempts.jsonl"), []byte(lines), 0600))
	})
}

func (p reportPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages)
}
func (p reportPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages)
}
func registerReportTools(ctx context.Context, rt *runtime.Runtime) error {
	specs := make([]tools.ToolSpec, 0, 3)
	specs = append(specs, tools.ToolSpec{Name: "report.lookup", Toolset: "report", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}, tools.ToolSpec{Name: "report.refund", Toolset: "report", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}})
	if err := rt.RegisterToolset(runtime.ToolsetRegistration{Name: "report", Specs: specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
		return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"ok": true}}), nil
	}}); err != nil {
		return err
	}
	if err := rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "child", Planner: reportPlanner{specs: specs}, Specs: specs, Workflow: engine.WorkflowDefinition{Name: "child.workflow", TaskQueue: "report", Handler: rt.ExecuteWorkflow}, PlanActivityName: "child.plan", ResumeActivityName: "child.resume", ExecuteToolActivity: "child.tool"}); err != nil {
		return err
	}
	delegate := tools.ToolSpec{Name: "delegate.child", Toolset: "delegate", IsAgentTool: true, AgentID: "child", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
	registration := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{Name: "delegate", AgentID: "child", Route: runtime.AgentRoute{ID: "child", WorkflowName: "child.workflow", DefaultTaskQueue: "report"}})
	registration.Specs = []tools.ToolSpec{delegate}
	if err := rt.RegisterToolset(registration); err != nil {
		return err
	}
	specs = append(specs, delegate)
	return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "report", Planner: reportPlanner{specs: specs}, Specs: specs, Workflow: engine.WorkflowDefinition{Name: "report.workflow", TaskQueue: "report", Handler: rt.ExecuteWorkflow}, PlanActivityName: "report.plan", ResumeActivityName: "report.resume", ExecuteToolActivity: "report.tool"})
}

func (p reportPlanner) complete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("report")
	if !ok {
		return nil, fmt.Errorf("report model missing")
	}
	definitions := make([]*model.ToolDefinition, len(p.specs))
	for i, spec := range p.specs {
		definitions[i] = &model.ToolDefinition{Name: string(spec.Name), InputSchema: map[string]any{"type": "object"}}
	}
	response, err := client.Complete(ctx, &model.Request{Model: "scripted", Messages: messages, Tools: definitions})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{}
	for _, call := range response.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: call.Name, ToolCallID: call.ID, Payload: call.Payload})
	}
	if len(result.ToolCalls) == 0 {
		result.FinalResponse = &planner.FinalResponse{Message: &response.Content[0]}
	}
	return result, nil
}
