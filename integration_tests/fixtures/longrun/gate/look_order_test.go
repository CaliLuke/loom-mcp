package suite_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"example.com/longrun/env"
	suite "example.com/longrun/gate"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookRefusesBeforeHarness(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, suite.WriteItems(dir, 29))
	f, err := suite.BuildFreeze(dir, "offline", []byte("calibration"), eval.VCS{Revision: "test"})
	require.NoError(t, err)
	require.NoError(t, suite.WriteFreeze(dir, f))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gate/calibration_default.json"), []byte("calibration"), 0600))
	for _, tc := range []struct {
		name  string
		look  int
		seed  bool
		fault bool
		want  string
	}{
		{"skip first", 2, false, false, "out-of-order"},
		{"skip second", 3, true, false, "out-of-order"},
		{"repeat decided", 1, true, false, "look already decided: CONTINUE"},
		{"advance after fail", 2, true, true, "later look refused after FAIL"},
		{"repeat failed", 1, true, true, "look already decided: FAIL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storeDir := t.TempDir()
			store, err := eval.Open(storeDir)
			require.NoError(t, err)
			ids := []string{}
			if tc.seed {
				ids = append(ids, seedLook(t, dir, store, f, tc.fault))
			}
			data, err := json.Marshal(ids)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "gate/looks_offline.json"), data, 0600))
			before, err := os.ReadDir(storeDir)
			require.NoError(t, err)
			// Begin creates an experiment before eval.Run can build any harness.
			// A poisoned binding and unchanged store prove refusal precedes that point.
			_, err = suite.Look(t.Context(), dir, "offline", "must-not-build", store, tc.look, 1)
			require.ErrorContains(t, err, tc.want)
			after, err := os.ReadDir(storeDir)
			require.NoError(t, err)
			assert.Equal(t, before, after, "no experiment or harness may be created")
		})
	}
}

func seedLook(t *testing.T, dir string, store *eval.Store, f gate.Freeze, fault bool) string {
	t.Helper()
	ss, err := suite.Scenarios(dir, "offline", "look1")
	require.NoError(t, err)
	cfg := eval.Config{Harness: func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
		panic("seed must not build harness")
	}, Scenarios: ss, Variants: scenarios.Variants(), Repeats: 2}
	label, err := f.Label(1)
	require.NoError(t, err)
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Label: label})
	require.NoError(t, err)
	keys, err := eval.Plan(cfg)
	require.NoError(t, err)
	items := map[string]eval.Item{}
	for _, s := range ss {
		for _, item := range s.Items.Items() {
			items[item.ID] = item
		}
	}
	for i, key := range keys {
		text := ""
		if fault && key.Variant != "baseline" {
			text = "BAD-99999"
		}
		rec := &runrecord.RunRecord{RootRunID: "seed", Runs: []runrecord.Run{{RunID: "seed", Transcript: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: text}}}}}}}
		m, err := env.Materialize(items[key.Item])
		require.NoError(t, err)
		measures, err := scenarios.Measures(m, []*runrecord.RunRecord{rec}, true)
		require.NoError(t, err)
		outcome := &eval.Outcome{Checks: measures.Checks, Scores: measures.Scores, Session: &agenttest.SessionResult{Runs: []*agenttest.Result{{RunID: "seed", Record: rec}}}}
		require.NoError(t, writer.Append(&eval.AttemptRecord{Index: i, AttemptKey: key, Outcome: outcome}))
	}
	return writer.ID()
}
