package eval_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerCancellationPreservesFirstStop(t *testing.T) {
	for _, stop := range []eval.StopReason{eval.StopTokenLimit, eval.StopLiveModelDisabled} {
		for _, repeats := range []int{1, 3} {
			t.Run(fmt.Sprintf("%s/repeats=%d", stop, repeats), func(t *testing.T) {
				cause := errors.New("caller stopped after first stop")
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				session := syntheticSession()
				session.Runs[0].Record = &runrecord.RunRecord{Runs: []runrecord.Run{{Calls: []runrecord.Call{{Completion: &callrecord.Completion{Usage: model.TokenUsage{TotalTokens: 1}}}}}}}
				cfg := eval.Config{Harness: emptyFactory(), Scenarios: []eval.Scenario{customScenario(session)}, Repeats: repeats, MaxTokens: 1}
				if stop == eval.StopLiveModelDisabled {
					cfg.Harness = func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
						return nil, agenttest.ErrLiveModelDisabled
					}
				}
				cfg.OnAttempt = func(*eval.AttemptRecord) {
					cancel(cause)
				}
				r, err := eval.Run(ctx, cfg)
				require.ErrorIs(t, err, cause)
				assert.Equal(t, stop, r.Stop)
				if stop == eval.StopLiveModelDisabled {
					require.ErrorIs(t, err, agenttest.ErrLiveModelDisabled)
				}
				for _, a := range r.Attempts[1:] {
					assert.Equal(t, stop, a.NotRun)
					assert.Nil(t, a.Outcome)
				}
			})
		}
	}
}

func TestRunnerOnAttemptPanicDrainsWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := planConfig()
		cfg.Variants = nil
		cfg.Scenarios = cfg.Scenarios[:1]
		cfg.Repeats = 3
		cfg.Concurrency = 2
		cfg.Harness = emptyFactory()
		started := make(chan struct{}, 6)
		release := make(chan struct{})
		cfg.Scenarios[0].Drive = func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
			started <- struct{}{}
			<-release
			return syntheticSession(), nil
		}
		var callbacks int
		var panicked *eval.AttemptRecord
		cfg.OnAttempt = func(a *eval.AttemptRecord) {
			panicked = a
			callbacks++
			panic("observer broke")
		}
		var report *eval.Report
		var runErr error
		var escaped any
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				escaped = recover()
			}()
			report, runErr = eval.Run(t.Context(), cfg)
		}()
		synctest.Wait()
		require.Len(t, started, 2)
		release <- struct{}{}
		synctest.Wait()
		select {
		case <-done:
			t.Error("Run returned before the in-flight attempt was drained")
		default:
		}
		close(release)
		synctest.Wait()
		assert.Nil(t, escaped, "OnAttempt panic escaped Run")
		require.NotNil(t, report)
		require.ErrorContains(t, runErr, "observer broke")
		require.ErrorContains(t, runErr, "goroutine")
		require.ErrorContains(t, runErr, "TestRunnerOnAttemptPanicDrainsWorkers")
		assert.Equal(t, eval.StopCallbackPanic, report.Stop)
		assert.Equal(t, 1, callbacks)
		assert.Equal(t, eval.VerdictError, panicked.Verdict())
		require.ErrorContains(t, panicked.HarnessErr, "observer broke")
		assert.Len(t, started, 2)
		assert.NotNil(t, report.Attempts[0].Outcome)
		assert.NotNil(t, report.Attempts[1].Outcome)
		for _, a := range report.Attempts[2:] {
			assert.Nil(t, a.Outcome)
			assert.Equal(t, report.Stop, a.NotRun)
		}
	})
}

func TestRunnerOnAttemptPanicForSkippedRecord(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := planConfig()
	var callbacks int
	cfg.OnAttempt = func(*eval.AttemptRecord) {
		callbacks++
		panic("skipped observer broke")
	}
	var report *eval.Report
	var err error
	require.NotPanics(t, func() {
		report, err = eval.Run(ctx, cfg)
	})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "skipped observer broke")
	require.ErrorContains(t, err, "goroutine")
	assert.Equal(t, eval.StopCanceled, report.Stop)
	assert.Equal(t, 1, callbacks)
	for _, a := range report.Attempts {
		assert.Nil(t, a.Outcome)
		assert.Equal(t, eval.StopCanceled, a.NotRun)
	}
}

func TestRunnerCancellationThenLiveDisabledJoinsErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		cause := errors.New("caller canceled before factory returned")
		release := make(chan struct{})
		cfg := eval.Config{Repeats: 2, Scenarios: []eval.Scenario{customScenario(syntheticSession())}}
		cfg.Harness = func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
			<-release
			return nil, agenttest.ErrLiveModelDisabled
		}
		var report *eval.Report
		var runErr error
		go func() {
			report, runErr = eval.Run(ctx, cfg)
		}()
		synctest.Wait()
		cancel(cause)
		synctest.Wait()
		close(release)
		synctest.Wait()
		require.ErrorIs(t, runErr, cause)
		require.ErrorIs(t, runErr, agenttest.ErrLiveModelDisabled)
		require.NotNil(t, report)
		assert.Equal(t, eval.StopCanceled, report.Stop)
		assert.Equal(t, eval.StopCanceled, report.Attempts[1].NotRun)
	})
}
