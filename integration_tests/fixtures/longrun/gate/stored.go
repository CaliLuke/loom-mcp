package suite

import (
	"fmt"
	"reflect"
	"slices"

	"example.com/longrun/env"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// Analyze evaluates stored rows with the complete planned cumulative manifest.
func Analyze(attempts []eval.StoredAttempt, cfg gate.Config, look int, previous []gate.Decision) (gate.Evaluation, gate.Decision, error) {
	table, err := gate.FromStored(attempts)
	if err != nil {
		return gate.Evaluation{}, gate.Decision{}, err
	}
	evaluation, err := gate.Evaluate(table, cfg, look)
	if err != nil {
		return evaluation, gate.Decision{}, err
	}
	decision, err := DecideLook(evaluation, previous)
	return evaluation, decision, err
}

// VerifyMeasures reconstructs scoring from exports without creating a harness.
func VerifyMeasures(a eval.StoredAttempt, item eval.Item) error {
	if a.HarnessError != "" || a.Error != "" || a.NotRun != "" || a.StorageError != "" {
		return nil
	}
	m, err := env.Materialize(item)
	if err != nil {
		return err
	}
	records := make([]*runrecord.RunRecord, 0, len(a.Runs))
	succeeded := true
	for _, run := range a.Runs {
		rec, err := run.Record()
		if err != nil {
			return err
		}
		records = append(records, rec)
		succeeded = succeeded && run.RunError == ""
	}
	recomputed, err := scenarios.Measures(m, records, succeeded)
	if err != nil {
		return err
	}
	for _, want := range recomputed.Checks {
		index := slices.IndexFunc(a.Checks, func(c expect.Check) bool { return c.Name == want.Name })
		if index < 0 || !reflect.DeepEqual(a.Checks[index], want) {
			return fmt.Errorf("%s: stored check differs: %s", a.Key, want.Name)
		}
	}
	for _, want := range recomputed.Scores {
		index := slices.IndexFunc(a.Scores, func(s expect.Score) bool { return s.Name == want.Name })
		if index < 0 || !reflect.DeepEqual(a.Scores[index], want) {
			return fmt.Errorf("%s: stored score differs: %s", a.Key, want.Name)
		}
	}
	return nil
}

// Overlay loads one rerun chain and replaces exact keys only after label verification.
func Overlay(store *eval.Store, id string, freeze gate.Freeze, look int) ([]eval.StoredAttempt, error) {
	rows, _, err := overlay(store, id, freeze, look, map[string]bool{})
	return rows, err
}
func overlay(store *eval.Store, id string, freeze gate.Freeze, look int, seen map[string]bool) ([]eval.StoredAttempt, map[eval.AttemptKey]bool, error) {
	if seen[id] {
		return nil, nil, fmt.Errorf("experiment parent cycle")
	}
	seen[id] = true
	exp, err := store.Load(id)
	if err != nil {
		return nil, nil, err
	}
	if err := freeze.VerifyLabel(exp.Header.Label, look); err != nil {
		return nil, nil, err
	}
	var previous []eval.StoredAttempt
	manifest := map[eval.AttemptKey]bool{}
	if exp.Header.Parent != "" {
		previous, manifest, err = overlay(store, exp.Header.Parent, freeze, look, seen)
		if err != nil {
			return nil, nil, err
		}
	} else {
		if exp.Header.Config.Selection.Keys != nil {
			return nil, nil, fmt.Errorf("initial look cannot be an exact-key rerun")
		}
		for _, v := range exp.Header.Variants {
			for _, s := range exp.Header.Scenarios {
				for _, item := range s.Items {
					for repeat := 1; repeat <= exp.Header.Config.Repeats; repeat++ {
						manifest[eval.AttemptKey{Variant: v.ID, Scenario: s.ID, Item: item, Repeat: repeat}] = true
					}
				}
			}
		}
	}
	byKey := map[eval.AttemptKey]eval.StoredAttempt{}
	for _, a := range previous {
		byKey[a.AttemptKey] = a
	}
	requested := manifest
	if exp.Header.Parent != "" {
		selection := exp.Header.Config.Selection
		if len(selection.Keys) == 0 || len(selection.Variants)+len(selection.Scenarios)+len(selection.Tags)+len(selection.Items) > 0 {
			return nil, nil, fmt.Errorf("parent rerun requires exact missing keys")
		}
		requested = map[eval.AttemptKey]bool{}
		for _, key := range selection.Keys {
			old, present := byKey[key]
			if requested[key] || !manifest[key] || (present && !infrastructureAttempt(old)) {
				return nil, nil, fmt.Errorf("rerun key is duplicate, unplanned or already valid: %s", key.String())
			}
			requested[key] = true
		}
	}
	// Validate the entire export before overlaying any observation.
	for _, a := range exp.Attempts {
		if !requested[a.AttemptKey] {
			return nil, nil, fmt.Errorf("unrequested child observation: %s", a.AttemptKey.String())
		}
	}
	for _, a := range exp.Attempts {
		byKey[a.AttemptKey] = *a
	}
	out := make([]eval.StoredAttempt, 0, len(byKey))
	for _, a := range byKey {
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b eval.StoredAttempt) int {
		if a.AttemptKey.String() < b.AttemptKey.String() {
			return -1
		}
		if a.AttemptKey == b.AttemptKey {
			return 0
		}
		return 1
	})
	return out, manifest, nil
}

func infrastructureAttempt(a eval.StoredAttempt) bool {
	return a.HarnessError != "" || a.Error != "" || a.NotRun != "" || a.StorageError != ""
}
