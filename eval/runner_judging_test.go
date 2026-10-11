package eval_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type outcomeJudge struct {
	prepared   atomic.Int32
	called     atomic.Int32
	prepareErr error
	judge      func(context.Context, *eval.Outcome) (eval.JudgeResult, error)
}

func TestRunnerJudgingLifecycle(t *testing.T) {
	j := &outcomeJudge{prepareErr: errors.New("calibration unavailable")}
	j.judge = func(_ context.Context, out *eval.Outcome) (eval.JudgeResult, error) {
		assert.Equal(t, int32(1), j.prepared.Load())
		return eval.JudgeResult{Judgments: []eval.Judgment{{ClaimID: "correct", Subject: eval.SubjectAnswer, Label: eval.Entailed}}, Usage: model.TokenUsage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}, Tokens: 3}, nil
	}
	s := customScenario(syntheticSession())
	cfg := eval.Config{Harness: emptyFactory(), Scenarios: []eval.Scenario{s}, Concurrency: 4, Repeats: 8, Judge: j}
	observed := 0
	cfg.OnAttempt = func(a *eval.AttemptRecord) {
		observed++
		assert.Equal(t, eval.VerdictPassed, a.Verdict())
		assert.Equal(t, 3, a.JudgeTokens)
	}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Equal(t, int32(1), j.prepared.Load())
	assert.Equal(t, int32(8), j.called.Load())
	assert.Equal(t, 8, observed)
	assert.Equal(t, 24, r.Tokens)
	require.NotNil(t, r.Judge)
	assert.Equal(t, "test", r.Judge.ModelID)
	assert.Equal(t, 0, r.Groups()[0].Pending)
	assert.Equal(t, 8, r.Groups()[0].Passed)
	assert.Equal(t, model.TokenUsage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}, r.Attempts[0].JudgeUsage)
}

func TestRunnerJudgeBudgetAndSkips(t *testing.T) {
	for _, path := range []string{"budget", "factory error", "close error", "outcome error", "failed check", "no claims"} {
		t.Run(path, func(t *testing.T) {
			j := &outcomeJudge{}
			j.judge = func(context.Context, *eval.Outcome) (eval.JudgeResult, error) {
				return eval.JudgeResult{Judgments: []eval.Judgment{{ClaimID: "correct", Label: eval.Entailed}}, Tokens: 7}, nil
			}
			session := syntheticSession()
			session.Runs[0].Record = &runrecord.RunRecord{Runs: []runrecord.Run{{Calls: []runrecord.Call{{Completion: &callrecord.Completion{Usage: model.TokenUsage{TotalTokens: 2}}}}}}}
			s := customScenario(session)
			cfg := eval.Config{Harness: emptyFactory(), Scenarios: []eval.Scenario{s}, Repeats: 2, Judge: j, MaxTokens: 9}
			switch path {
			case "factory error":
				cfg.Harness = func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
					return nil, errors.New("factory failed")
				}
			case "close error":
				cfg.Harness = eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
					return nil
				}, nil, agenttest.ScriptedModel("unused", agenttest.Respond(agenttest.Text("unused"))))
			case "outcome error":
				cfg.Scenarios[0].Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
					return eval.Assertions{}, errors.New("assert failed")
				}
			case "failed check":
				cfg.Scenarios[0].Assert = func(context.Context, *eval.Attempt) (eval.Assertions, error) {
					return eval.Assertions{Checks: []expect.Check{{Name: "quality", Diagnostic: "failed"}}}, nil
				}
			case "no claims":
				cfg.Scenarios[0].Claims = nil
				cfg.Scenarios[0].Expect = []expect.Expectation{expect.MaxModelCalls(0)}
			}
			r, err := eval.Run(t.Context(), cfg)
			require.NoError(t, err)
			if path == "budget" || path == "failed check" {
				assert.Equal(t, int32(1), j.called.Load())
				assert.Equal(t, eval.StopTokenLimit, r.Stop)
				assert.Equal(t, eval.VerdictNotRun, r.Attempts[1].Verdict())
			} else {
				assert.Zero(t, j.called.Load())
			}
		})
	}
}

func TestRunnerRejectsNegativeJudgeTimeout(t *testing.T) {
	_, err := eval.Plan(eval.Config{Harness: emptyFactory(), Scenarios: []eval.Scenario{customScenario(syntheticSession())}, JudgeTimeout: -time.Second})
	require.ErrorContains(t, err, "JudgeTimeout")
}

func (j *outcomeJudge) Prepare(context.Context) error {
	j.prepared.Add(1)
	return j.prepareErr
}
func (j *outcomeJudge) JudgeOutcome(ctx context.Context, out *eval.Outcome) (eval.JudgeResult, error) {
	j.called.Add(1)
	return j.judge(ctx, out)
}
func (j *outcomeJudge) Info() eval.JudgeInfo {
	return eval.JudgeInfo{ModelID: "test"}
}
