package eval_test

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/require"
)

func TestStoreExample(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	cfg := eval.Config{Harness: emptyFactory(), Scenarios: []eval.Scenario{{ID: "example", Items: eval.Items(eval.Item{ID: "one", Input: "hello"}), Drive: func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
		return &agenttest.SessionResult{}, nil
	}, Assert: func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		return eval.Assertions{Checks: []expect.Check{{Name: "example", Passed: true}}}, nil
	}}}}
	run := func() error {
		// snippet:start eval-store
		store, err := eval.Open(dir)
		if err != nil {
			return err
		}
		writer, err := store.Begin(ctx, cfg, eval.ExperimentMeta{Label: "baseline"})
		if err != nil {
			return err
		}
		cfg.OnAttempt = writer.OnAttempt(cfg.OnAttempt)
		report, runErr := eval.Run(ctx, cfg)
		return errors.Join(runErr, writer.Finish(report, runErr))
		// snippet:end eval-store
	}
	require.NoError(t, run())
}
