package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cReportIneligibleAndUnavailable(t *testing.T) {
	for _, eligible := range []string{"receipt_only", "hybrid"} {
		t.Run(eligible, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, WriteItems(dir, 19))
			f, _ := frozenEvidenceFixture(t)
			ineligible := "receipt_only"
			if eligible == ineligible {
				ineligible = "hybrid"
			}
			delete(f.Policy.Unsupported, ineligible)
			for i := range f.Selections {
				if f.Selections[i].Variant == ineligible {
					f.Selections[i] = gate.CandidateSelection{Variant: ineligible, Status: gate.CalibrationIneligible}
				}
			}
			missing := projectionStoredReport(t, dir, f, false)
			observed := projectionStoredReport(t, dir, f, true)
			assert.Equal(t, observed.Decisions, missing.Decisions)
			assert.Equal(t, observed.Evaluations, missing.Evaluations)
			require.Len(t, missing.Decisions, 2)
			assert.Equal(t, gate.Continue, missing.Decisions[0].Status)
			require.Len(t, missing.Decisions[0].Variants, 1)
			assert.Equal(t, eligible, missing.Decisions[0].Variants[0].Variant)
			a, err := PlanLook(f.Policy, 3, missing.Decisions)
			require.NoError(t, err)
			b, err := PlanLook(f.Policy, 3, observed.Decisions)
			require.NoError(t, err)
			assert.Equal(t, b, a)
			assert.Equal(t, []string{"baseline", eligible}, a.Variants)
			data, err := json.Marshal(missing)
			require.NoError(t, err)
			var wire struct {
				Variants    []gate.VariantDecision `json:"variants"`
				Diagnostics map[string]struct {
					Available bool `json:"available"`
					Attempts  int  `json:"attempts"`
				} `json:"diagnostics"`
			}
			require.NoError(t, json.Unmarshal(data, &wire))
			assert.Contains(t, wire.Variants, gate.VariantDecision{Variant: ineligible, Status: gate.CalibrationIneligible})
			assert.Contains(t, wire.Variants, missing.Decisions[1].Variants[0])
			for _, key := range []string{"baseline/model_change", eligible + "/model_change", "clear_only/model_change", "clear_only/fact_early"} {
				d, ok := wire.Diagnostics[key]
				assert.True(t, ok, "planned result must remain visible: %s", key)
				assert.False(t, d.Available, key)
				assert.Zero(t, d.Attempts, key)
				assert.Positive(t, observed.Diagnostics[key].Attempts, key)
			}
			assert.NotContains(t, wire.Diagnostics, ineligible+"/model_change")
			assert.Equal(t, 24, observed.Diagnostics["clear_only/model_change"].Attempts, "clear-only is scheduled at look one only")
			assert.Equal(t, 48, observed.Diagnostics[eligible+"/model_change"].Attempts)
			data, err = json.Marshal(observed)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &wire))
			assert.True(t, wire.Diagnostics["clear_only/model_change"].Available)
		})
	}
}

// Persist reserved measures directly to exercise report reconstruction without a
// provider or harness. Informational quality failures must not change scheduling.
func projectionStoredReport(t *testing.T, dir string, f gate.Freeze, informational bool) Report {
	t.Helper()
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	var ids []string
	var out Report
	for look := 1; look <= 2; look++ {
		work, err := PlanLook(f.Policy, look, out.Decisions)
		require.NoError(t, err)
		ss, err := Scenarios(dir, "offline", fmt.Sprintf("look%d", look))
		require.NoError(t, err)
		variants := make([]eval.Variant, 0, len(work.Variants))
		for _, id := range work.Variants {
			variants = append(variants, eval.Variant{ID: id})
		}
		cfg := eval.Config{Harness: func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
			t.Fatal("stored report constructed a harness")
			return nil, nil
		}, Scenarios: ss, Variants: variants, Repeats: 2}
		label, err := f.Label(look)
		require.NoError(t, err)
		writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Label: label})
		require.NoError(t, err)
		keys, err := eval.Plan(cfg)
		require.NoError(t, err)
		for i, key := range keys {
			info := key.Variant == "clear_only" || key.Scenario == "model_change"
			if info && !informational {
				continue
			}
			// One failed item per scenario keeps the active variant inconclusive.
			s := ss[slices.IndexFunc(ss, func(s eval.Scenario) bool { return s.ID == key.Scenario })]
			success := !info && (key.Variant == "baseline" || key.Item != s.Items.Items()[0].ID)
			require.NoError(t, writer.Append(&eval.AttemptRecord{AttemptKey: key, Index: i, Outcome: &eval.Outcome{
				Checks: []expect.Check{{Name: "task_success", Passed: success}, {Name: "no_unsupported_claim", Passed: true}},
				Scores: []expect.Score{{Name: "evidence_used", Value: 1}, {Name: "recall_qualifying", Value: 1}, {Name: "recall_success", Value: 1}},
			}}))
		}
		ids = append(ids, writer.ID())
		out, err = reportLooks(dir, "offline", store, f, ids, false)
		require.NoError(t, err)
	}
	return out
}
