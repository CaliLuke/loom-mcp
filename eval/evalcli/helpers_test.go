package evalcli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/require"
)

func config(t *testing.T) evalcli.Config {
	t.Helper()
	return evalcli.Config{
		StoreDir: t.TempDir(),
		Harness: eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
			return nil
		}, nil),
		Scenarios: []eval.Scenario{{
			ID: "s", Tags: []string{"red"},
			Items: eval.Items(eval.Item{ID: "pass", Input: "hello"}, eval.Item{ID: "fail", Input: "hello"}),
			Drive: func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
				return &agenttest.SessionResult{}, nil
			},
			Assert: func(_ context.Context, a *eval.Attempt) (eval.Assertions, error) {
				check := expect.Check{Name: "quality", Passed: a.Item.ID != "fail"}
				if !check.Passed {
					check.Diagnostic = "fixture check"
				}
				return eval.Assertions{Checks: []expect.Check{check}}, nil
			},
		}},
	}
}

func invoke(ctx context.Context, cfg evalcli.Config, args ...string) (int, string, string) {
	var out, diagnostic bytes.Buffer
	cfg.Stdout, cfg.Stderr = &out, &diagnostic
	code := evalcli.Main(ctx, args, cfg)
	return code, out.String(), diagnostic.String()
}

func seed(t *testing.T, cfg evalcli.Config) *eval.Experiment {
	t.Helper()
	store, err := eval.Open(cfg.StoreDir)
	require.NoError(t, err)
	runner := eval.Config{Harness: cfg.Harness, Scenarios: cfg.Scenarios, Variants: cfg.Variants}
	w, err := store.Begin(t.Context(), runner, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	runner.OnAttempt = w.OnAttempt(nil)
	report, err := eval.Run(t.Context(), runner)
	require.NoError(t, err)
	require.NoError(t, w.Finish(report, err))
	e, err := store.Load(w.ID())
	require.NoError(t, err)
	return e
}

func experiments(t *testing.T, dir string) (*eval.Store, []eval.ExperimentHeader) {
	t.Helper()
	s, err := eval.Open(dir)
	require.NoError(t, err)
	all, err := s.List()
	require.NoError(t, err)
	return s, all
}
