package control_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlConfigRejectsAmbiguousHostCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*control.Config)
	}{
		{"scope", func(c *control.Config) { c.Scope = "" }},
		{"candidate store", func(c *control.Config) { c.Candidates = nil }},
		{"experiment store", func(c *control.Config) { c.Experiments = nil }},
		{"authorizer", func(c *control.Config) { c.Authorize = nil }},
		{"read cap", func(c *control.Config) { c.Limits.MaxReadBytes = 0 }},
		{"page cap", func(c *control.Config) { c.Limits.MaxPageEntries = 0 }},
		{"role", func(c *control.Config) { c.Role = "unknown" }},
		{"empty grants", func(c *control.Config) { c.Operations = nil }},
		{"duplicate grant", func(c *control.Config) { c.Operations = append(c.Operations, control.CreateOperation) }},
		{"unknown grant", func(c *control.Config) { c.Operations = append(c.Operations, "run_shell") }},
		{"selector writes", func(c *control.Config) { c.Role = inspect.Selector }},
		{"missing executor", func(c *control.Config) { c.Executor = nil }},
		{"missing evidence", func(c *control.Config) { c.Operations = append(c.Operations, control.ReadEvidenceOperation) }},
		{"task identity", func(c *control.Config) { c.Tasks[0].Task.ID = "" }},
		{"task snapshot", func(c *control.Config) { c.Tasks[0].Snapshot = "" }},
		{"binding version", func(c *control.Config) { c.Tasks[0].HostBinding.Version = "" }},
		{"duplicate task", func(c *control.Config) { c.Tasks = append(c.Tasks, c.Tasks[0]) }},
		{"empty modes", func(c *control.Config) { c.Tasks[0].Task.Modes = nil }},
		{"unknown mode", func(c *control.Config) { c.Tasks[0].Task.Modes = []eval.ExecutionKind{"execute"} }},
		{"duplicate mode", func(c *control.Config) { c.Tasks[0].Task.Modes = append(c.Tasks[0].Task.Modes, eval.ExecutionProbe) }},
		{"evaluation without plan", func(c *control.Config) { c.Tasks[0].Task.Modes = []eval.ExecutionKind{eval.ExecutionEvaluation} }},
		{"probe with plan", func(c *control.Config) { c.Tasks[0].Task.EvaluationPlans = []string{"plan"} }},
		{"duplicate plans", func(c *control.Config) {
			c.Tasks[0].Task.Modes = []eval.ExecutionKind{eval.ExecutionEvaluation}
			c.Tasks[0].Task.EvaluationPlans = []string{"plan", "plan"}
		}},
		{"empty plan", func(c *control.Config) {
			c.Tasks[0].Task.Modes = []eval.ExecutionKind{eval.ExecutionEvaluation}
			c.Tasks[0].Task.EvaluationPlans = []string{""}
		}},
		{"component binding", func(c *control.Config) { c.Tasks[0].ComponentBindings = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config(t)
			tc.change(&cfg)
			_, err := control.New(cfg)
			code(t, err, control.InvalidInput)
		})
	}
}
func TestControlConfigSnapshotsAndAbsentCapabilities(t *testing.T) {
	cfg := config(t)
	cfg.Operations = []control.Operation{control.DiscoverOperation}
	cfg.Executor = nil
	s := service(t, cfg)
	cfg.Tasks[0].Task.Modes[0] = eval.ExecutionEvaluation
	cfg.Tasks[0].ComponentBindings[0].Version = "mutated"
	cfg.Operations[0] = control.CancelOperation
	got, err := s.Discover(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []control.Operation{control.DiscoverOperation}, got.Operations)
	assert.Equal(t, []eval.ExecutionKind{eval.ExecutionProbe}, got.Tasks[0].Modes)
	got.Tasks[0].Modes[0] = eval.ExecutionEvaluation
	again, err := s.Discover(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []eval.ExecutionKind{eval.ExecutionProbe}, again.Tasks[0].Modes)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.Discover(ctx)
	require.ErrorIs(t, err, context.Canceled)
}
