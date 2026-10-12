package gate

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

// FromStored converts persisted reserved measures without reconstructing a
// harness. Overlay Parent reruns before calling: Evaluate rejects duplicate keys.
// Missing/nonbinary measures are errors, never implicit quality failures.
func FromStored(attempts []eval.StoredAttempt) (Table, error) {
	rows := make(Table, 0, len(attempts))
	for _, a := range attempts {
		r := Row{Key: Key{a.Variant, a.Scenario, a.Item, a.Repeat}, Infrastructure: a.HarnessError != "" || a.Error != "" || a.NotRun != "" || a.StorageError != ""}
		for _, run := range a.Runs {
			r.RunFailed = r.RunFailed || run.RunError != ""
		}
		if !r.Infrastructure {
			var err error
			r, err = storedMeasures(a, r)
			if err != nil {
				return nil, err
			}
		}

		rows = append(rows, r)
	}
	return rows, nil
}

func storedMeasures(a eval.StoredAttempt, r Row) (Row, error) {
	checks := map[string]bool{}
	for _, c := range a.Checks {
		if _, ok := checks[c.Name]; ok {
			return r, fmt.Errorf("gate: duplicate check %s", c.Name)
		}
		checks[c.Name] = c.Passed
	}
	scores := map[string]float64{}
	for _, s := range a.Scores {
		if _, ok := scores[s.Name]; ok {
			return r, fmt.Errorf("gate: duplicate score %s", s.Name)
		}
		scores[s.Name] = s.Value
	}
	success, ok := checks["task_success"]
	if !ok {
		return r, fmt.Errorf("gate: missing task_success for %s", a.Key)
	}
	supported, ok := checks["no_unsupported_claim"]
	if !ok {
		return r, fmt.Errorf("gate: missing no_unsupported_claim for %s", a.Key)
	}
	for _, name := range []string{"evidence_used", "recall_qualifying", "recall_success"} {
		v, ok := scores[name]
		if !ok || (v != 0 && v != 1) {
			return r, fmt.Errorf("gate: missing or nonbinary %s for %s", name, a.Key)
		}
	}
	r.Success = success
	r.Unsupported = !supported
	r.Evidence = scores["evidence_used"] == 1
	r.Qualifying = scores["recall_qualifying"] == 1
	r.Recalled = scores["recall_success"] == 1
	return r, nil
}
