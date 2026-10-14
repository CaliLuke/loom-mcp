package suite

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"example.com/longrun/env"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportNeedsNoHarnessAndChecksLabels(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteItems(dir, 19))
	f, _ := frozenEvidenceFixture(t)
	require.NoError(t, WriteFreeze(dir, f))
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, root.Close()) })
	require.NoError(t, root.WriteFile("gate/calibration_default.json", []byte("calibration"), 0600))
	pools, err := env.Generate(19)
	require.NoError(t, err)
	calls := 0
	cfg := eval.Config{Harness: func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
		calls++
		panic("report must not construct a harness")
	}, Scenarios: []eval.Scenario{scenarios.New("fact_early", eval.Items(pools["fact_early/look1"]...))}, Variants: scenarios.Variants(), Repeats: 2}
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	label, err := f.Label(1)
	require.NoError(t, err)
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Label: label})
	require.NoError(t, err)
	data, err := json.Marshal([]string{writer.ID()})
	require.NoError(t, err)
	require.NoError(t, root.WriteFile("gate/looks_offline.json", data, 0600))
	report, err := reportLooks(dir, "offline", store, f, []string{writer.ID()}, true)
	require.NoError(t, err)
	require.Len(t, report.Decisions, 1)
	assert.Equal(t, gate.Incomplete, report.Decisions[0].Status)
	assert.Zero(t, calls)
	wrong, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Label: "unfrozen"})
	require.NoError(t, err)
	_, err = Overlay(store, wrong.ID(), f, 1)
	assert.ErrorContains(t, err, "label mismatch")
}
