package gate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func simulateLooks(table Table, cfg Config, out *CellReport, eval evaluator) error {
	var previous []Decision
	active := slices.Clone(cfg.Variants)
	lastSize := 0
	var final Decision
	for l, n := range cfg.Policy.Looks {
		rows, err := syntheticPrefix(table, n)
		if err != nil {
			return err
		}
		current := cfg
		current.Variants = slices.Clone(active)
		e, err := eval(rows, current, l+1)
		if err != nil {
			return err
		}
		d, err := Decide(e, previous)
		if err != nil {
			return err
		}
		out.ExpectedAttempts += float64((n - lastSize) * (1 + len(active)) * len(cfg.Scenarios) * cfg.Policy.Repeats)
		lastSize = n
		final = d
		active = nil
		for _, v := range d.Variants {
			if v.Look == l+1 && terminal(v.Status) {
				recordVariantStop(out, v)
			}
			if v.Status == Continue {
				active = append(active, v.Variant)
			}
			if v.Status == Incomplete {
				return fmt.Errorf("gate: synthetic population unexpectedly incomplete")
			}
		}
		if d.Status == Complete {
			break
		}
		previous = append(previous, d)
	}
	recordCombinedStop(out, final, lastSize, len(cfg.Scenarios))
	return nil
}

// recordCombinedStop retains configuration outcomes and total scheduled cost.
func recordCombinedStop(out *CellReport, final Decision, lastSize, scenarios int) {
	allPass := true
	for _, v := range final.Variants {
		allPass = allPass && v.Status == Pass
	}
	if allPass {
		out.Pass++
		out.LookPass[final.Look-1]++
		for j := final.Look - 1; j < 2; j++ {
			out.TruncatedPass[j]++
		}
	}
	for _, v := range final.Variants {
		if v.Status == Fail {
			out.LookFail[final.Look-1]++
			break
		}
	}
	for _, v := range final.Variants {
		if v.Status == NotPassed {
			out.NotPassed++
			break
		}
	}
	out.ExpectedLooks += float64(final.Look)
	out.ExpectedItems += float64(lastSize * scenarios)
}

func recordVariantStop(out *CellReport, v VariantDecision) {
	if out.Variants == nil {
		out.Variants = map[string]VariantSimulation{}
	}
	if out.VariantPass == nil {
		out.VariantPass = map[string]float64{}
	}
	if out.Final == nil {
		out.Final = map[string]map[Status]float64{}
	}
	stats := out.Variants[v.Variant]
	if stats.FailureMasks == nil {
		stats.FailureMasks = map[string]float64{}
	}
	switch v.Status {
	case Pass:
		stats.Pass++
		stats.LookPass[v.Look-1]++
		out.VariantPass[v.Variant]++
	case Fail:
		stats.LookFail[v.Look-1]++
	case NotPassed:
		stats.NotPassed++
	case Continue, Incomplete, Inconclusive, Complete, CalibrationIneligible, CalibrationEligible:
	}
	causes := slices.Clone(v.FailedCriteria)
	if len(v.GuardFailures) > 0 {
		stats.GuardStop++
		causes = append(causes, "guard")
	}
	if len(v.FailedCriteria) > 0 {
		stats.CriterionStop++
	}
	if len(causes) > 0 {
		slices.Sort(causes)
		stats.FailureMasks[strings.Join(causes, "+")]++
	}
	out.Variants[v.Variant] = stats
	for _, c := range criteria(v.Evidence) {
		key := v.Variant + "/" + c.name
		if out.Final[key] == nil {
			out.Final[key] = map[Status]float64{}
		}
		out.Final[key][c.result.Status]++
	}
}
func syntheticPrefix(table Table, n int) (Table, error) {
	rows := make(Table, 0, len(table))
	for _, r := range table {
		id, err := strconv.Atoi(r.Item)
		if err != nil {
			return nil, err
		}
		if id < n {
			rows = append(rows, r)
		}
	}
	return rows, nil
}
