package suite

import (
	"context"
	"fmt"
	"slices"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/fake"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// DryGate evaluates disjoint smoke items; their outcomes never enter production looks.
// Stored-only equivalence and export verification have separate acceptance tests.
func DryGate(ctx context.Context, dir string, faults fake.Faults, workers int) (Report, error) {
	cfg := GateConfig(env.GatedScenarios("offline"))
	cfg.Items = map[string][]string{}
	variants := slices.DeleteFunc(scenarios.Variants(), func(v eval.Variant) bool { return v.ID == "clear_only" })
	var rows []eval.StoredAttempt
	diagnostics := map[string]Diagnostics{}
	for _, id := range env.GatedScenarios("offline") {
		d, err := LoadPool(dir, id, "smoke")
		if err != nil {
			return Report{}, err
		}
		for _, item := range d.Items() {
			cfg.Items[id] = append(cfg.Items[id], item.ID)
		}
		factory := func(ctx context.Context, v eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
			selected := faults
			if v.ID == "baseline" {
				selected = fake.Faults{}
			}
			return longrun.Harness("offline", selected)(ctx, v, opts...)
		}
		experiment, err := eval.Run(ctx, eval.Config{Harness: factory, Scenarios: []eval.Scenario{scenarios.New(id, d)}, Variants: variants, Repeats: 2, Concurrency: workers})
		if err != nil {
			return Report{}, err
		}
		for _, a := range experiment.Attempts {
			if a.HarnessErr != nil {
				return Report{}, fmt.Errorf("dry attempt incomplete: %s: %w", a.AttemptKey.String(), a.HarnessErr)
			}
			if a.Outcome == nil {
				return Report{}, fmt.Errorf("dry attempt has no outcome: %s", a.AttemptKey.String())
			}
			if a.Outcome.Err != nil {
				return Report{}, fmt.Errorf("dry attempt incomplete: %s: %w", a.AttemptKey.String(), a.Outcome.Err)
			}
			row := eval.StoredAttempt{AttemptKey: a.AttemptKey, Checks: a.Outcome.Checks, Scores: a.Outcome.Scores}
			for _, r := range a.Outcome.Session.Runs {
				run := eval.StoredRun{}
				if r.RunErr != nil {
					run.RunError = r.RunErr.Error()
				}
				row.Runs = append(row.Runs, run)
			}
			key := a.Variant + "/" + a.Scenario
			diag := diagnostics[key]
			diag.Attempts++
			if diag.Scores == nil {
				diag.Scores = map[string]float64{}
			}
			for _, score := range row.Scores {
				diag.Scores[score.Name] += score.Value
			}
			diagnostics[key] = diag
			rows = append(rows, row)
		}
	}
	evaluation, decision, err := Analyze(rows, cfg, 1, nil)
	if err != nil {
		return Report{}, err
	}
	return Report{Evaluations: []gate.Evaluation{evaluation}, Decisions: []gate.Decision{decision}, Diagnostics: diagnostics}, nil
}
