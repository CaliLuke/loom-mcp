package enginetest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
)

// TestKnownGapCompletion runs fake scenarios in subprocesses to observe actual
// testing.T failures without failing the parent test that expects them.
func TestKnownGapCompletion(t *testing.T) {
	const marker = "LOOM_MCP_TEST_GAP_COMPLETION"
	const incomplete = "known gap scenario did not complete"
	gap := func() error {
		return &GapError{Err: errors.New("observed behavioral difference")}
	}
	cases := []struct {
		name    string
		timeout bool
		run     func(context.Context, context.CancelFunc) error
		failure string
	}{
		{name: "behavioral_failure", run: func(context.Context, context.CancelFunc) error {
			return fmt.Errorf("observed: %w", gap())
		}},
		{name: "untyped_error", failure: "known gap scenario must return a typed behavioral failure", run: func(context.Context, context.CancelFunc) error {
			return errors.New("backend unavailable")
		}},
		{name: "obsolete_gap", failure: "known gap no longer reproduces: remove it from the list", run: func(context.Context, context.CancelFunc) error {
			return nil
		}},
		{name: "blocked_deadline", timeout: true, failure: incomplete, run: func(ctx context.Context, _ context.CancelFunc) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		{name: "cancellation", failure: incomplete, run: func(context.Context, context.CancelFunc) error {
			return context.Canceled
		}},
		{name: "wrapped_deadline", failure: incomplete, run: func(context.Context, context.CancelFunc) error {
			return fmt.Errorf("observed: %w", &GapError{Err: context.DeadlineExceeded})
		}},
		{name: "wrapped_cancellation", failure: incomplete, run: func(context.Context, context.CancelFunc) error {
			return &GapError{Err: context.Canceled}
		}},
		{name: "blocked_deadline_returns_gap", timeout: true, failure: incomplete, run: func(ctx context.Context, _ context.CancelFunc) error {
			<-ctx.Done()
			return gap()
		}},
		{name: "canceled_context_returns_gap", failure: incomplete, run: func(_ context.Context, cancel context.CancelFunc) error {
			cancel()
			return gap()
		}},
		{name: "canceled_context_returns_nil", failure: incomplete, run: func(_ context.Context, cancel context.CancelFunc) error {
			cancel()
			return nil
		}},
	}
	if selected := os.Getenv(marker); selected != "" {
		for _, tc := range cases {
			if tc.name != selected {
				continue
			}
			bound := 10 * time.Second
			if tc.timeout {
				bound = time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), bound)
			t.Cleanup(cancel)
			runScenario(t, ctx, Factory{
				New: func(*testing.T) Fixture {
					return Fixture{Engine: inmem.New()}
				},
				KnownGaps: map[string]string{tc.name: "fake behavioral gap"},
			}, scenario{name: tc.name, run: func(_ *testing.T, f Fixture) error {
				return tc.run(f.ctx, cancel)
			}})
			return
		}
		t.Fatalf("unknown fake scenario %q", selected)
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestKnownGapCompletion$", "-test.short", "-test.v", "-test.timeout=15s")
			cmd.Env = append(os.Environ(), marker+"="+tc.name)
			output, err := cmd.CombinedOutput()
			if tc.failure == "" {
				require.NoError(t, err, "%s", output)
				assert.Contains(t, string(output), "EXPECTED_FAILURE["+tc.name+"]")
				return
			}
			require.Error(t, err, "%s", output)
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Contains(t, string(output), tc.failure)
			assert.NotContains(t, string(output), "EXPECTED_FAILURE[")
		})
	}
}
