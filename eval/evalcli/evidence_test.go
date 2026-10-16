package evalcli_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
)

func TestRunCaptureEvidenceFactories(t *testing.T) {
	for _, attemptAware := range []bool{false, true} {
		name := "legacy"
		if attemptAware {
			name = "attempt"
		}
		t.Run(name, func(t *testing.T) {
			cfg := config(t)
			cfg.CaptureEvidence = true
			legacy := cfg.Harness
			var dirs []string
			factory := func(ctx context.Context, variant eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
				h, err := legacy(ctx, variant, opts...)
				if h != nil {
					dirs = append(dirs, h.EvidenceDir())
				}
				return h, err
			}
			cfg.Harness = factory
			if attemptAware {
				cfg.Harness = nil
				cfg.AttemptHarness = func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
					return factory(ctx, info.Variant, opts...)
				}
			}
			code, _, diagnostic := invoke(t.Context(), cfg, "run", "--item", "pass", "--repeat", "2")
			require.Equal(t, 0, code, diagnostic)
			require.Len(t, dirs, 2)
			assert.NotEmpty(t, dirs[0])
			assert.NotEmpty(t, dirs[1])
			assert.NotEqual(t, dirs[0], dirs[1])
			for _, dir := range dirs {
				_, err := agenttest.ReadEvidence(t.Context(), dir)
				require.NoError(t, err)
			}
			store, headers := experiments(t, cfg.StoreDir)
			require.Len(t, headers, 1)
			evidence, err := store.ReadEvidence(t.Context(), headers[0].ID)
			require.NoError(t, err)
			require.Len(t, evidence, 2)
			for i, attempt := range evidence {
				assert.Equal(t, eval.AttemptKey{Variant: "default", Scenario: "s", Item: "pass", Repeat: i + 1}, attempt.Key)
				assert.Empty(t, attempt.Error)
				require.NotNil(t, attempt.Snapshot)
			}
		})
	}
}

func TestRunCaptureEvidenceRejectsIgnoredOptions(t *testing.T) {
	cfg := config(t)
	cfg.CaptureEvidence = true
	legacy := cfg.Harness
	cfg.Harness = func(ctx context.Context, variant eval.Variant, _ ...agenttest.Option) (*agenttest.Harness, error) {
		return legacy(ctx, variant)
	}
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--item", "pass")
	assert.Equal(t, 1, code)
	assert.Contains(t, diagnostic, "evidence")
}
