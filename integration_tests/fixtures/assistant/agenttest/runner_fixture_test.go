package agenttest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func runnerScenario() eval.Scenario {
	return eval.Scenario{ID: "status", Agent: "assistant.assistant_runtime", Items: eval.Items(eval.Item{ID: "one", Input: "status?", ExpectedTools: &expect.ExpectedTools{Exact: true}})}
}
func runnerFactory(base ...agenttest.Option) eval.HarnessFunc {
	return eval.NewHarnessFunc(registerScenarioFixture, func(h *agenttest.Harness, _ eval.Variant) error {
		h.Passthrough(projected.ProjectedStatusToolTool)
		return nil
	}, base...)
}

func TestRunnerRepeatsFixture(t *testing.T) {
	s := runnerScenario()
	s.Items = eval.Items(eval.Item{ID: "one", Input: "status?", ExpectedTools: &expect.ExpectedTools{Exact: true, Tools: []expect.ExpectedTool{{Name: projected.ProjectedStatusTool}}}})
	var built int
	cfg := eval.Config{Scenarios: []eval.Scenario{s}, Repeats: 5, Harness: func(ctx context.Context, v eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		built++
		turns := []agenttest.Turn{agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})), agenttest.Respond(agenttest.Text("ready"))}
		wrong := built == 2 || built == 4
		if wrong {
			turns = []agenttest.Turn{agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool, &projected.ProjectedLookupToolPayload{Query: "wrong"})), agenttest.Respond(agenttest.Text("ready"))}
		}
		factory := eval.NewHarnessFunc(guideRegister, func(h *agenttest.Harness, _ eval.Variant) error {
			if wrong {
				agenttest.Stub(h, projected.ProjectedLookupToolTool).Return(&projected.ProjectedLookupToolResult{Answer: "wrong", Source: "stub"})
			} else {
				agenttest.Stub(h, projected.ProjectedStatusToolTool).Return(&projected.ProjectedStatusToolResult{Status: "ready"})
			}
			return nil
		}, agenttest.ScriptedModel("fixture", turns...))
		return factory(ctx, v, opts...)
	}}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	for _, a := range r.Attempts {
		require.NoError(t, a.HarnessErr)
		require.NoError(t, a.Outcome.Err)
	}
	assert.Equal(t, []eval.Group{{Variant: "default", Scenario: "status", Item: "one", Attempts: 5, Passed: 3, Failed: 2}}, r.Groups())
}

func TestRunnerInfrastructureFixture(t *testing.T) {
	for _, tc := range []struct {
		name    string
		turns   []agenttest.Turn
		strict  bool
		verdict eval.Verdict
	}{
		{"unstubbed", []agenttest.Turn{agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})), agenttest.Respond(agenttest.Text("ready"))}, true, eval.VerdictError},
		{"agent failure", []agenttest.Turn{agenttest.Fail(errors.New("model broke"))}, false, eval.VerdictFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := runnerFactory(agenttest.ScriptedModel("fixture", tc.turns...))
			if tc.strict {
				factory = eval.NewHarnessFunc(registerScenarioFixture, nil, agenttest.ScriptedModel("fixture", tc.turns...))
			}
			r, err := eval.Run(t.Context(), eval.Config{Harness: factory, Scenarios: []eval.Scenario{runnerScenario()}})
			require.NoError(t, err)
			a := r.Attempts[0]
			assert.Equal(t, tc.verdict, a.Verdict())
			if tc.strict {
				var stub *agenttest.StubError
				require.ErrorAs(t, a.Outcome.Err, &stub)
				require.ErrorAs(t, a.HarnessErr, &stub)
				assert.Len(t, a.Runs(), 1)
			} else {
				assert.NoError(t, a.Outcome.Err)
				assert.NoError(t, a.HarnessErr)
			}
		})
	}
}

func TestRunnerVariantFinalsFixture(t *testing.T) {
	r, err := eval.Run(t.Context(), eval.Config{Harness: runnerFactory(), Scenarios: []eval.Scenario{runnerScenario()}, Variants: []eval.Variant{
		{ID: "first", Options: []agenttest.Option{agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("first")))}},
		{ID: "second", Options: []agenttest.Option{agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("second")))}},
	}})
	require.NoError(t, err)
	for _, a := range r.Attempts {
		require.NoError(t, a.HarnessErr)
		assert.Equal(t, a.Variant, a.Outcome.Session.Runs[0].Output.Final.Parts[0].(model.TextPart).Text)
	}
}

func TestRunnerVariantPolicyFixture(t *testing.T) {
	s := runnerScenario()
	s.Items = eval.Items(eval.Item{ID: "one", Input: "status?", ExpectedTools: &expect.ExpectedTools{}})
	script := agenttest.ScriptedModel("fixture",
		agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{}), agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})),
		agenttest.Respond(agenttest.Text("ready")))
	r, err := eval.Run(t.Context(), eval.Config{Harness: runnerFactory(), Scenarios: []eval.Scenario{s}, Variants: []eval.Variant{
		{ID: "unlimited", Options: []agenttest.Option{script}},
		{ID: "cap", Policy: &api.PolicyOverrides{PerTurnMaxToolCalls: 1}, Options: []agenttest.Option{script}},
	}})
	require.NoError(t, err)
	counts := make([]int, 2)
	for i, a := range r.Attempts {
		require.NoError(t, a.HarnessErr)
		require.NoError(t, a.Outcome.Err)
		for _, step := range a.Outcome.Session.Trajectory[0].Steps {
			if step.Tool != nil && step.Tool.Error == nil {
				counts[i]++
			}
		}
	}
	// Runtime admits provider batches atomically; the cap rejects the entire batch.
	assert.Equal(t, []int{2, 0}, counts)
}

func TestRunnerVariantCompactionFixture(t *testing.T) {
	script := agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready")))
	r, err := eval.Run(t.Context(), eval.Config{Harness: runnerFactory(), Scenarios: []eval.Scenario{runnerScenario()}, Variants: []eval.Variant{
		{ID: "off", Options: []agenttest.Option{script}},
		{ID: "on", Options: []agenttest.Option{script, agenttest.WithRuntimeOptions(runtime.WithContextCompaction("assistant.assistant_runtime", runtime.ContextCompaction{}), runtime.WithContextLimits(map[string]model.ContextLimits{"fixture": {ContextWindow: 200000}}))}},
	}})
	require.NoError(t, err)
	require.NoError(t, r.Attempts[0].HarnessErr)
	require.NoError(t, r.Attempts[1].HarnessErr)
	require.NoError(t, r.Attempts[0].Outcome.Err)
	require.NoError(t, r.Attempts[1].Outcome.Err)
	assert.Nil(t, r.Attempts[0].Runs()[0].Export.Runs[0].CompactionSettings)
	assert.NotNil(t, r.Attempts[1].Runs()[0].Export.Runs[0].CompactionSettings)
}

func TestRunnerTokenLimitFixture(t *testing.T) {
	cfg := eval.Config{Harness: runnerFactory(agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready"), agenttest.Usage(60, 40)))), Scenarios: []eval.Scenario{runnerScenario()}, Repeats: 5, MaxTokens: 250}
	var callbacks int
	cfg.OnAttempt = func(*eval.AttemptRecord) {
		callbacks++
	}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Equal(t, 300, r.Tokens)
	assert.Equal(t, eval.StopTokenLimit, r.Stop)
	assert.Equal(t, 5, callbacks)
	for _, a := range r.Attempts[:3] {
		assert.Equal(t, 100, a.Tokens)
		assert.Equal(t, 60, a.Usage.InputTokens)
		assert.Equal(t, 40, a.Usage.OutputTokens)
		assert.Empty(t, a.Usage.Model)
		assert.Equal(t, eval.VerdictPassed, a.Verdict())
	}
	for _, a := range r.Attempts[3:] {
		assert.Equal(t, eval.StopTokenLimit, a.NotRun)
		assert.Nil(t, a.Outcome)
	}
}

func TestRunnerMultiTurnExportFixture(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		assert.NoError(t, provider.Shutdown(context.Background()))
	})
	s := runnerScenario()
	s.Items = eval.Items(eval.Item{ID: "two", Turns: []string{"hi", "again"}, ExpectedTools: &expect.ExpectedTools{Exact: true}})
	r, err := eval.Run(t.Context(), eval.Config{Harness: runnerFactory(agenttest.WithTracerProvider(provider), agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("first"), agenttest.Usage(60, 40)), agenttest.Respond(agenttest.Text("second"), agenttest.Usage(60, 40)))), Scenarios: []eval.Scenario{s}})
	require.NoError(t, err)
	a := r.Attempts[0]
	require.NoError(t, a.HarnessErr)
	assert.Equal(t, 200, a.Tokens)
	require.Len(t, a.Runs(), 2)
	evidence := a.Runs()
	assert.Equal(t, evidence[0].SessionID, evidence[1].SessionID)
	for _, e := range evidence {
		assert.NotEmpty(t, e.TraceID)
		data, err := e.Export.Marshal()
		require.NoError(t, err)
		decoded, err := runrecord.FromExport(data)
		require.NoError(t, err)
		assert.Equal(t, e.RunID, decoded.RootRunID)
	}
}

func TestRunnerDeterminismFixture(t *testing.T) {
	cfg := eval.Config{Harness: runnerFactory(agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready"), agenttest.Usage(60, 40)))), Scenarios: []eval.Scenario{runnerScenario()}, Repeats: 12, Concurrency: 4}
	first, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	second, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Equal(t, first.Tokens, second.Tokens)
	for i, a := range first.Attempts {
		b := second.Attempts[i]
		assert.Equal(t, a.AttemptKey, b.AttemptKey)
		assert.Equal(t, a.Verdict(), b.Verdict())
		assert.Equal(t, a.Outcome.Checks, b.Outcome.Checks)
		assert.Equal(t, a.Outcome.Scores, b.Outcome.Scores)
		assert.Equal(t, a.Tokens, b.Tokens)
	}
}

func TestRunnerClonesPolicyPerRunFixture(t *testing.T) {
	policy := &api.PolicyOverrides{DeniedTags: []string{"absent"}, PerToolTimeout: map[tools.Ident]time.Duration{"projected.projected_status_tool": time.Second}}
	var originalValues []time.Duration
	interceptor := runtime.RuntimeInterceptorFuncs{BeforeRunFunc: func(_ context.Context, in *runtime.BeforeRunInput) (*runtime.BeforeRunDecision, error) {
		originalValues = append(originalValues, in.Input.Policy.PerToolTimeout[projected.ProjectedStatusTool])
		in.Input.Policy.PerToolTimeout[projected.ProjectedStatusTool] = 2 * time.Second
		in.Input.Policy.DeniedTags[0] = "also_absent"
		return nil, nil
	}}
	cfg := eval.Config{Harness: runnerFactory(agenttest.WithRuntimeOptions(runtime.WithInterceptors(interceptor))), Scenarios: []eval.Scenario{runnerScenario()}, Repeats: 2, Variants: []eval.Variant{{ID: "policy", Policy: policy, Options: []agenttest.Option{agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready")))}}}}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	for _, a := range r.Attempts {
		require.NoError(t, a.HarnessErr)
		require.NoError(t, a.Outcome.Err)
	}
	assert.Equal(t, []time.Duration{time.Second, time.Second}, originalValues)
	assert.Equal(t, time.Second, policy.PerToolTimeout[projected.ProjectedStatusTool])
	assert.Equal(t, []string{"absent"}, policy.DeniedTags)
}

func TestRunnerMatrixFixture(t *testing.T) {
	first := runnerScenario()
	first.Items = eval.Items(eval.Item{ID: "a", Input: "hi", ExpectedTools: &expect.ExpectedTools{}}, eval.Item{ID: "b", Input: "hello", ExpectedTools: &expect.ExpectedTools{}})
	second := first
	second.ID = "second"
	script := agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready")))
	cfg := eval.Config{Harness: runnerFactory(), Scenarios: []eval.Scenario{first, second}, Variants: []eval.Variant{{ID: "v1", Options: []agenttest.Option{script}}, {ID: "v2", Options: []agenttest.Option{script}}}, Repeats: 3}
	keys, err := eval.Plan(cfg)
	require.NoError(t, err)
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, r.Attempts, 24)
	for i, a := range r.Attempts {
		assert.Equal(t, keys[i], a.AttemptKey)
		assert.Equal(t, i, a.Index)
		assert.Equal(t, eval.VerdictPassed, a.Verdict())
		require.NoError(t, a.HarnessErr)
	}
}

func TestRunnerPreservesDefaultPolicyFixture(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy *api.PolicyOverrides
		want   int
	}{
		{name: "nil inherits default", want: 2},
		{name: "explicit overrides default", policy: &api.PolicyOverrides{PerTurnMaxToolCalls: 1}, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observed []int
			interceptor := runtime.RuntimeInterceptorFuncs{BeforeRunFunc: func(_ context.Context, in *runtime.BeforeRunInput) (*runtime.BeforeRunDecision, error) {
				if in.Input.Policy == nil {
					observed = append(observed, 0)
				} else {
					observed = append(observed, in.Input.Policy.PerTurnMaxToolCalls)
				}
				return nil, nil
			}}
			r, err := eval.Run(t.Context(), eval.Config{
				Harness: runnerFactory(
					agenttest.WithDefaultRunOptions(runtime.WithPerTurnMaxToolCalls(2)),
					agenttest.WithRuntimeOptions(runtime.WithInterceptors(interceptor)),
					agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready")))),
				Scenarios: []eval.Scenario{runnerScenario()},
				Variants:  []eval.Variant{{ID: "policy", Policy: tc.policy}},
			})
			require.NoError(t, err)
			require.NoError(t, r.Attempts[0].HarnessErr)
			require.NoError(t, r.Attempts[0].Outcome.Err)
			assert.Equal(t, []int{tc.want}, observed)
		})
	}
}
