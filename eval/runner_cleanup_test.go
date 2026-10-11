package eval_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupDiagnostic counts rendering of a retained script diagnostic. These
// scenarios never run a model: only Harness.Close renders that diagnostic.
// This observes repeated Close calls without adding a production testing hook.
type cleanupDiagnostic struct {
	rendered atomic.Int32
}

func TestRunnerClosesEachHarnessExactlyOnce(t *testing.T) {
	for _, path := range []string{"complete", "drive error", "factory error", "setup error", "setup panic", "reuse"} {
		t.Run(path, func(t *testing.T) {
			var probes []*cleanupDiagnostic
			var reused *agenttest.Harness
			cfg := eval.Config{Scenarios: []eval.Scenario{customScenario(syntheticSession())}, Repeats: 3}
			cfg.Harness = func(ctx context.Context, v eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
				if path == "reuse" && reused != nil {
					return reused, nil
				}
				probe := &cleanupDiagnostic{}
				probes = append(probes, probe)
				script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("unused")).When(func(*model.Request) error {
					return probe
				}))
				if err != nil {
					return nil, err
				}
				// Retain one matcher diagnostic without rendering it here.
				if _, err := script.Complete(ctx, &model.Request{}); !errors.Is(err, probe) {
					return nil, errors.New("cleanup probe was not retained")
				}
				factory := eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
					return nil
				}, func(*agenttest.Harness, eval.Variant) error {
					switch path {
					case "setup error":
						return errors.New("setup rejected")
					case "setup panic":
						panic("setup panicked")
					}
					return nil
				}, agenttest.ScriptedModelFrom("cleanup", script))
				h, err := factory(ctx, v, opts...)
				if path == "factory error" {
					return h, errors.Join(err, errors.New("factory rejected"))
				}
				reused = h
				return h, err
			}
			if path == "drive error" {
				cfg.Scenarios[0].Drive = func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
					return nil, errors.New("drive failed")
				}
			}
			report, err := eval.Run(t.Context(), cfg)
			require.NoError(t, err)
			require.Len(t, report.Attempts, 3)
			wantHarnesses := 3
			if path == "reuse" {
				wantHarnesses = 1
			}
			require.Len(t, probes, wantHarnesses)
			for i, probe := range probes {
				assert.Equal(t, int32(1), probe.rendered.Load(), "harness %d cleanup count", i)
			}
		})
	}
}

func (d *cleanupDiagnostic) Error() string {
	d.rendered.Add(1)
	return "cleanup probe"
}
