package agenttest_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"testing"

	generated "example.com/assistant/gen/assistant/agents/assistant_runtime"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type storeFixturePlanner struct {
	system string
	parent bool
}

func TestStoreMatrixFixture(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		assert.NoError(t, provider.Shutdown(context.Background()))
	})
	factory := func(ctx context.Context, v eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		base := []agenttest.Option{agenttest.WithTracerProvider(provider), agenttest.ScriptedModel("fixture",
			agenttest.Respond(agenttest.Text("one"), agenttest.Usage(2, 1)), agenttest.Respond(agenttest.Text("two"), agenttest.Usage(2, 1)), agenttest.Respond(agenttest.Text("three"), agenttest.Usage(2, 1)))}
		return eval.NewHarnessFunc(storeFixtureRegister(v.Labels["system"]), nil, base...)(ctx, v, opts...)
	}
	scenario := eval.Scenario{ID: "chat", Agent: "parent", Task: "help", Reference: "ready", Items: eval.Items(eval.Item{ID: "multi", Turns: []string{"hello", "again", "last"}}, eval.Item{ID: "child", Input: "delegate"}), Expect: []expect.Expectation{expect.Completed()}, Claims: []eval.Claim{{ID: "helpful", Text: "Helpful", Informational: true}}, Assert: func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		return eval.Assertions{Scores: []expect.Score{{Name: "quality", Value: 1}}}, nil
	}}
	cfg := eval.Config{Harness: factory, Scenarios: []eval.Scenario{scenario}, Repeats: 2, Concurrency: 2, Variants: []eval.Variant{
		{ID: "base", Labels: map[string]string{"system": "base"}},
		{ID: "changed", Labels: map[string]string{"system": "changed"}, Options: []agenttest.Option{agenttest.WithRuntimeOptions(runtime.WithContextCompaction("parent", runtime.ContextCompaction{}))}},
	}}
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	cfg.OnAttempt = writer.OnAttempt(nil)
	report, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, writer.Finish(report, nil))
	experiment, err := store.Load(writer.ID())
	require.NoError(t, err)
	require.Len(t, experiment.Attempts, 8)
	for i, a := range experiment.Attempts {
		original := report.Attempts[i]
		require.NoError(t, original.HarnessErr)
		require.NoError(t, original.Outcome.Err)
		assert.Equal(t, original.AttemptKey, a.AttemptKey)
		assert.Equal(t, i, a.Index)
		assert.Equal(t, original.Verdict(), a.Verdict)
		assert.Equal(t, original.Usage, a.Usage)
		assert.Equal(t, original.Tokens, a.Tokens)
		assert.True(t, original.Started.Equal(a.Started))
		assert.Equal(t, original.Duration, a.Duration)
		assert.Equal(t, original.Outcome.Checks, a.Checks)
		assert.Equal(t, original.Outcome.Scores, a.Scores)
		assert.Equal(t, []eval.StoredClaim{{ID: "helpful", Text: "Helpful", Informational: true}}, a.Claims)
		assert.Equal(t, original.Outcome.Task, a.Task)
		assert.Equal(t, original.Outcome.Reference, a.Reference)
		assert.Empty(t, a.HarnessError)
		assert.Empty(t, a.Error)
		assertStoreTrajectories(t, a, original)
		if a.Item == "child" {
			rec, err := a.Runs[0].Record()
			require.NoError(t, err)
			assert.Len(t, rec.Runs, 2)
		}
	}
	base, changed := experiment.Header.Fingerprints["base"], experiment.Header.Fingerprints["changed"]
	assert.NotEqual(t, base.SystemHashes, changed.SystemHashes)
	assert.NotEqual(t, base.Context, changed.Context)
	assert.Equal(t, experiment.Attempts[0].Fingerprint.SystemHashes, experiment.Attempts[1].Fingerprint.SystemHashes)
	assert.Equal(t, []prompt.PromptRef{{ID: "a", Version: "1"}, {ID: "z", Version: "2"}}, base.Prompts)
}

func TestStoreTrajectoryExceptionsFixture(t *testing.T) {
	for _, tc := range []struct {
		name       string
		turn       agenttest.Turn
		unverified bool
	}{{"intercepted", agenttest.Respond(agenttest.Text("done")), true}, {"run-error", agenttest.Fail(errors.New("model broke")), false}} {
		t.Run(tc.name, func(t *testing.T) {
			opts := []agenttest.Option{agenttest.ScriptedModel("fixture", tc.turn)}
			if tc.unverified {
				opts = append(opts, agenttest.WithRuntimeOptions(runtime.WithInterceptors(runtime.RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *runtime.BeforeEventInput) (*runtime.BeforeEventDecision, error) {
					return nil, nil
				}})))
			}
			cfg := eval.Config{Harness: eval.NewHarnessFunc(storeFixtureRegister("same"), nil, opts...), Scenarios: []eval.Scenario{{ID: "chat", Agent: "parent", Items: eval.Items(eval.Item{ID: "one", Input: "hello"}), Expect: []expect.Expectation{expect.Completed()}}}}
			store, err := eval.Open(t.TempDir())
			require.NoError(t, err)
			writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
			require.NoError(t, err)
			report, err := eval.Run(t.Context(), cfg)
			require.NoError(t, err)
			require.NoError(t, writer.Finish(report, nil))
			e, err := store.Load(writer.ID())
			require.NoError(t, err)
			assert.Equal(t, tc.unverified, e.Attempts[0].Runs[0].EventsUnverified)
			if !tc.unverified {
				assert.NotEmpty(t, e.Attempts[0].Runs[0].RunError)
			}
			assertStoreTrajectories(t, e.Attempts[0], report.Attempts[0])
		})
	}
}

func assertStoreTrajectories(t *testing.T, a *eval.StoredAttempt, original *eval.AttemptRecord) {
	t.Helper()
	session, err := a.Session()
	require.NoError(t, err)
	require.Len(t, a.Runs, len(original.Outcome.Session.Runs))
	for i, run := range a.Runs {
		prior := original.Outcome.Session.Runs[i]
		export, err := prior.Record.Export().Marshal()
		require.NoError(t, err)
		assert.Equal(t, string(export), string(run.Export))
		if prior.RunErr != nil {
			assert.Equal(t, prior.RunErr.Error(), run.RunError)
		} else {
			assert.Empty(t, run.RunError)
		}
		assert.Equal(t, prior.RunID, run.RunID)
		assert.Equal(t, prior.SessionID, run.SessionID)
		assert.Equal(t, prior.TraceID, run.TraceID)
		rec, err := run.Record()
		require.NoError(t, err)
		assert.Equal(t, prior.Record.RootRunID, rec.RootRunID)
		tr, err := run.Trajectory()
		require.NoError(t, err)
		got, err := tr.Marshal()
		require.NoError(t, err)
		want, err := prior.Trajectory.Marshal()
		require.NoError(t, err)
		assert.Equal(t, string(want), string(got))
		sessionBytes, err := session[i].Marshal()
		require.NoError(t, err)
		assert.Equal(t, string(want), string(sessionBytes))
	}
}

func storeFixtureRegister(system string) agenttest.Register {
	return func(ctx context.Context, rt *runtime.Runtime) error {
		if err := generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: storeFixturePlanner{system: system}}); err != nil {
			return err
		}
		spec := tools.ToolSpec{Name: "delegate.child", Toolset: "delegate", IsAgentTool: true, AgentID: "assistant.assistant_runtime", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
		reg := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{Name: "delegate", AgentID: "assistant.assistant_runtime", Route: runtime.AgentRoute{ID: "assistant.assistant_runtime", WorkflowName: generated.WorkflowName, DefaultTaskQueue: generated.DefaultTaskQueue}})
		reg.Specs = []tools.ToolSpec{spec}
		if err := rt.RegisterToolset(reg); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "parent", Planner: storeFixturePlanner{system: system, parent: true}, Specs: []tools.ToolSpec{spec}, Workflow: engine.WorkflowDefinition{Name: "parent.workflow", TaskQueue: "parent", Handler: rt.ExecuteWorkflow}, PlanActivityName: "parent.plan", ResumeActivityName: "parent.resume", ExecuteToolActivity: "parent.tool"})
	}
}

func (p storeFixturePlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	result, err := p.complete(ctx, in.Agent, in.Messages)
	if err != nil {
		return nil, err
	}
	if p.parent && len(in.Messages) > 0 && len(in.Messages[len(in.Messages)-1].Parts) > 0 {
		if text, ok := in.Messages[len(in.Messages)-1].Parts[0].(model.TextPart); ok && text.Text == "delegate" {
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "delegate.child", ToolCallID: "child", Payload: rawjson.Message(`{}`)}}}, nil
		}
	}
	return result, nil
}
func (p storeFixturePlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages)
}
func (p storeFixturePlanner) complete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("fixture")
	if !ok {
		return nil, fmt.Errorf("fixture model missing")
	}
	input := append([]*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: p.system}}}}, messages...)
	response, err := client.Complete(ctx, &model.Request{Model: "scripted", Messages: input, PromptRefs: []prompt.PromptRef{{ID: "z", Version: "2"}, {ID: "a", Version: "1"}, {ID: "z", Version: "2"}}})
	if err != nil {
		return nil, err
	}
	final := response.Content[0]
	final.Meta = map[string]any{"exact": jsontext.Value(`9007199254740993`)}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &final}}, nil
}

func TestStoreRunnerStopsFixture(t *testing.T) {
	for _, stop := range []eval.StopReason{eval.StopTokenLimit, eval.StopCanceled} {
		t.Run(string(stop), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cfg := eval.Config{Harness: runnerFactory(agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("done"), agenttest.Usage(2, 1)))), Scenarios: []eval.Scenario{runnerScenario()}, Repeats: 3}
			if stop == eval.StopTokenLimit {
				cfg.MaxTokens = 3
			}
			store, err := eval.Open(t.TempDir())
			require.NoError(t, err)
			writer, err := store.Begin(ctx, cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
			require.NoError(t, err)
			cfg.OnAttempt = writer.OnAttempt(func(*eval.AttemptRecord) {
				if stop == eval.StopCanceled {
					cancel()
				}
			})
			report, runErr := eval.Run(ctx, cfg)
			if stop == eval.StopCanceled {
				require.ErrorIs(t, runErr, context.Canceled)
			} else {
				require.NoError(t, runErr)
			}
			require.NoError(t, writer.Finish(report, runErr))
			e, err := store.Load(writer.ID())
			require.NoError(t, err)
			assert.Equal(t, "stopped", e.Header.Status)
			assert.Equal(t, stop, e.Header.Stop)
			assert.Equal(t, 3, e.Header.Tokens)
			assert.True(t, e.Header.Finished.Equal(report.Finished))
			assert.True(t, e.Complete())
			require.Len(t, e.Attempts, 3)
			assert.Equal(t, eval.VerdictNotRun, e.Attempts[1].Verdict)
		})
	}
}
