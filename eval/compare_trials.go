package eval

import "fmt"

// TrialComparison pools independent trials without assigning a synthetic experiment identity.
// Totals include only shared scenario/item cases; repeats are pooled, never paired.
type TrialComparison struct {
	Warnings      []Warning    `json:"warnings"`
	OnlyBaseline  []Pair       `json:"only_baseline"`
	OnlyCandidate []Pair       `json:"only_candidate"`
	Groups        []GroupDelta `json:"groups"`
	Totals        Totals       `json:"totals"`
}

type trialIdentity struct {
	experiment string
	attempt    AttemptKey
}

// CompareTrials compares nonempty selections of complete single-attempt experiments.
// Each original experiment/attempt may appear once across both selections. Variant
// names and repeat numbers remain local to their experiments; cases match by
// scenario/item. Callers own authorization, selection limits and binding checks.
func CompareTrials(base, cand []Side) (*TrialComparison, error) {
	seen := make(map[trialIdentity]bool)
	baseline, err := resolveTrials(base, seen)
	if err != nil {
		return nil, err
	}
	candidate, err := resolveTrials(cand, seen)
	if err != nil {
		return nil, err
	}
	warnings, err := trialWarnings(baseline, candidate)
	if err != nil {
		return nil, err
	}
	compared := alignComparison(&Comparison{}, trialGroups(baseline), trialGroups(candidate))
	return &TrialComparison{Warnings: warnings, OnlyBaseline: compared.OnlyBaseline, OnlyCandidate: compared.OnlyCandidate, Groups: compared.Groups, Totals: compared.Totals}, nil
}

// resolveTrials retains original sources and rejects aliases before aggregation.
func resolveTrials(input []Side, seen map[trialIdentity]bool) ([]Side, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("eval: trial comparison requires nonempty selections")
	}
	resolved := make([]Side, 0, len(input))
	for _, source := range input {
		side, err := resolveSide(source)
		if err != nil {
			return nil, err
		}
		e := side.Experiment
		if e.Header.ID == "" || !e.Complete() || len(e.Attempts) != 1 || e.Attempts[0].Variant != side.Variant {
			return nil, fmt.Errorf("eval: trial comparison requires complete single-attempt experiments")
		}
		key := trialIdentity{experiment: e.Header.ID, attempt: e.Attempts[0].AttemptKey}
		if seen[key] {
			return nil, fmt.Errorf("eval: duplicate trial association")
		}
		seen[key] = true
		resolved = append(resolved, side)
	}
	return resolved, nil
}

// trialGroups pools original attempts by case without rewriting their lineage.
func trialGroups(sides []Side) []reportGroup {
	var groups []reportGroup
	positions := make(map[Pair]int)
	for _, side := range sides {
		a := side.Experiment.Attempts[0]
		pair := Pair{Scenario: a.Scenario, Item: a.Item}
		index, ok := positions[pair]
		if !ok {
			index = len(groups)
			positions[pair] = index
			groups = append(groups, reportGroup{Scenario: a.Scenario, Item: a.Item})
		}
		groups[index].attempts = append(groups[index].attempts, a)
	}
	for i := range groups {
		groups[i].GroupStats = groupStats(groups[i].attempts)
	}
	return groups
}

// trialWarnings evaluates existing warnings against every original matched source.
// An unchanged-agent qualification applies only when all matched sources agree.
func trialWarnings(base, cand []Side) ([]Warning, error) {
	var warnings []Warning
	seen := make(map[Warning]bool)
	unchanged, matched := true, false
	for _, b := range base {
		for _, c := range cand {
			ba, ca := b.Experiment.Attempts[0], c.Experiment.Attempts[0]
			if ba.Scenario != ca.Scenario || ba.Item != ca.Item {
				continue
			}
			matched = true
			pair, err := comparisonWarnings(b, c, c.Experiment.Attempts, nil)
			if err != nil {
				return nil, err
			}
			same := false
			for _, w := range pair {
				if w.Code == warningNoAgentChange {
					same = true
					continue
				}
				if !seen[w] {
					warnings = append(warnings, w)
					seen[w] = true
				}
			}
			unchanged = unchanged && same
		}
	}
	if matched && unchanged {
		warnings = append(warnings, Warning{Code: warningNoAgentChange, Message: "recorded agent fingerprints, models and policy are identical"})
	}
	return warnings, nil
}
