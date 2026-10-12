package eval

import (
	"context"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestContextPolicyCloneOwnsWindow(t *testing.T) {
	window := 24000
	p := &api.PolicyOverrides{Context: &compaction.ContextOverrides{ContextWindow: &window}}
	owned := clonePolicy(p)
	window = 8000
	require.NotNil(t, owned.Context)
	assert.Equal(t, 24000, *owned.Context.ContextWindow)
}
func TestContextFingerprintIncludesChildren(t *testing.T) {
	rec := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", ContextFingerprint: "window24k"}, {RunID: "child", ContextFingerprint: "window8k"}, {RunID: "other", ContextFingerprint: "window8k"}}}
	a, err := recordFingerprint(rec)
	require.NoError(t, err)
	assert.Equal(t, []string{"window24k", "window8k"}, a.ContextFingerprints)
	rec.Runs[1].ContextFingerprint = "window16k"
	b, err := recordFingerprint(rec)
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
}

func TestRunSnapshotsContextBeforeHarnessDispatch(t *testing.T) {
	window := 24000
	cfg := Config{Repeats: 2, Variants: []Variant{{ID: "hybrid", Policy: &api.PolicyOverrides{Context: &compaction.ContextOverrides{ContextWindow: &window}}}}, Scenarios: []Scenario{{ID: "s", Assert: func(context.Context, *Attempt) (Assertions, error) { return Assertions{}, nil }, Items: Items(Item{ID: "one", Input: "hello"}), Drive: func(context.Context, *agenttest.Harness, Item) (*agenttest.SessionResult, error) {
		return &agenttest.SessionResult{}, nil
	}}}}
	cfg.Harness = func(ctx context.Context, v Variant, options ...agenttest.Option) (*agenttest.Harness, error) {
		window = 8000
		assert.Equal(t, 24000, *v.Policy.Context.ContextWindow)
		return agenttest.NewHarness(ctx, func(context.Context, *runtime.Runtime) error { return nil }, options...)
	}
	report, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Len(t, report.Attempts, 2)
}
