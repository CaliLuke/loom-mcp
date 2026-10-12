package eval

import (
	"fmt"
	"slices"
)

// Compare compares two distinct experiments restricted to selected variants.
func Compare(base, cand Side, opts CompareOptions) (*Comparison, error) {
	return compareSides(base, cand, opts, false)
}

// CompareExperiments compares only variants shared by both experiments, except
// that two single-variant experiments compare even when their variant IDs differ.
func CompareExperiments(base, cand *Experiment, opts CompareOptions) ([]*Comparison, error) {
	if base == nil || cand == nil {
		return nil, fmt.Errorf("eval: compare requires two experiments")
	}
	if sameExperiment(base, cand) {
		return nil, fmt.Errorf("eval: compare an experiment's variants with CompareVariants")
	}
	if len(base.Header.Variants) == 1 && len(cand.Header.Variants) == 1 {
		c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, opts)
		if err != nil {
			return nil, err
		}
		return []*Comparison{c}, nil
	}
	var comparisons []*Comparison
	var unmatched []Warning
	for _, v := range cand.Header.Variants {
		if !hasVariant(base, v.ID) {
			unmatched = append(unmatched, unmatchedVariant(cand, v.ID))
			continue
		}
		c, err := Compare(Side{Experiment: base, Variant: v.ID}, Side{Experiment: cand, Variant: v.ID}, opts)
		if err != nil {
			return nil, err
		}
		comparisons = append(comparisons, c)
	}
	for _, v := range base.Header.Variants {
		if !hasVariant(cand, v.ID) {
			unmatched = append(unmatched, unmatchedVariant(base, v.ID))
		}
	}
	if len(comparisons) == 0 {
		return nil, fmt.Errorf("eval: variant_unmatched: experiments have no shared variants")
	}
	for _, c := range comparisons {
		c.Warnings = append(c.Warnings, unmatched...)
	}
	return comparisons, nil
}

// CompareVariants compares the first named variant to every subsequent variant.
func CompareVariants(e *Experiment, variants []string) ([]*Comparison, error) {
	if len(variants) < 2 {
		return nil, fmt.Errorf("eval: need at least two variants")
	}
	seen := make(map[string]bool)
	for _, v := range variants {
		if v == "" || seen[v] {
			return nil, fmt.Errorf("eval: duplicate or empty variant %q", v)
		}
		seen[v] = true
	}
	comparisons := make([]*Comparison, 0, len(variants)-1)
	for _, variant := range variants[1:] {
		c, err := compareSides(Side{Experiment: e, Variant: variants[0]}, Side{Experiment: e, Variant: variant}, CompareOptions{}, true)
		if err != nil {
			return nil, err
		}
		comparisons = append(comparisons, c)
	}
	return comparisons, nil
}

func compareSides(base, cand Side, opts CompareOptions, allowSame bool) (*Comparison, error) {
	var err error
	if base, err = resolveSide(base); err != nil {
		return nil, err
	}
	if cand, err = resolveSide(cand); err != nil {
		return nil, err
	}
	if !allowSame && sameExperiment(base.Experiment, cand.Experiment) {
		return nil, fmt.Errorf("eval: compare an experiment's variants with CompareVariants")
	}
	attempts, err := mergedAttempts(cand.Experiment, opts.Parent)
	if err != nil {
		return nil, err
	}
	bg, cg := sideGroups(base, base.Experiment.Attempts), sideGroups(cand, attempts)
	c := &Comparison{Baseline: sideInfo(base), Candidate: sideInfo(cand)}
	c.Warnings, err = comparisonWarnings(base, cand, attempts, opts.Parent)
	if err != nil {
		return nil, err
	}
	return alignComparison(c, bg, cg), nil
}

func alignComparison(c *Comparison, bg, cg []reportGroup) *Comparison {
	baseline := make(map[Pair]reportGroup)
	for _, g := range bg {
		baseline[Pair{g.Scenario, g.Item}] = g
	}
	candidate := make(map[Pair]bool)
	var baseAttempts, candAttempts []*StoredAttempt
	for _, g := range cg {
		pair := Pair{g.Scenario, g.Item}
		candidate[pair] = true
		b, ok := baseline[pair]
		if !ok {
			c.OnlyCandidate = append(c.OnlyCandidate, pair)
			continue
		}
		c.Groups = append(c.Groups, compareGroup(b, g))
		baseAttempts = append(baseAttempts, b.attempts...)
		candAttempts = append(candAttempts, g.attempts...)
	}
	for _, g := range bg {
		pair := Pair{g.Scenario, g.Item}
		if !candidate[pair] {
			c.OnlyBaseline = append(c.OnlyBaseline, pair)
		}
	}
	c.Totals = Totals{Baseline: groupStats(baseAttempts), Candidate: groupStats(candAttempts)}
	for _, g := range c.Groups {
		c.Totals.Baseline.ToolsUnavailable += g.Baseline.ToolsUnavailable
		c.Totals.Candidate.ToolsUnavailable += g.Candidate.ToolsUnavailable
	}
	return c
}

func resolveSide(side Side) (Side, error) {
	if side.Experiment == nil {
		return Side{}, fmt.Errorf("eval: compare requires an experiment")
	}
	if side.Variant == "" {
		if len(side.Experiment.Header.Variants) != 1 {
			return Side{}, fmt.Errorf("eval: select one variant for %s", side.Experiment.Header.ID)
		}
		side.Variant = side.Experiment.Header.Variants[0].ID
	}
	if !hasVariant(side.Experiment, side.Variant) {
		return Side{}, fmt.Errorf("eval: unknown variant %q in %s", side.Variant, side.Experiment.Header.ID)
	}
	return side, nil
}

func sameExperiment(base, cand *Experiment) bool {
	return base == cand || (base.Header.ID != "" && base.Header.ID == cand.Header.ID)
}

func hasVariant(e *Experiment, variant string) bool {
	return slices.ContainsFunc(e.Header.Variants, func(v StoredVariant) bool {
		return v.ID == variant
	})
}

func sideInfo(side Side) SideInfo {
	e := side.Experiment
	return SideInfo{ID: e.Header.ID, Label: e.Header.Label, Variant: side.Variant, Status: e.Header.Status, Complete: e.Complete()}
}

// sideGroups uses header declaration order; parent-only groups follow candidate groups.
func sideGroups(side Side, attempts []*StoredAttempt) []reportGroup {
	var selected []*StoredAttempt
	for _, a := range attempts {
		if a.Variant == side.Variant {
			selected = append(selected, a)
		}
	}
	groups := reportGroups(selected)
	ordered := make([]reportGroup, 0, len(groups))
	used := make(map[Pair]bool)
	for _, scenario := range side.Experiment.Header.Scenarios {
		for _, item := range scenario.Items {
			for _, g := range groups {
				if g.Scenario == scenario.ID && g.Item == item {
					ordered = append(ordered, g)
					used[Pair{g.Scenario, g.Item}] = true
				}
			}
		}
	}
	for _, g := range groups {
		if !used[Pair{g.Scenario, g.Item}] {
			ordered = append(ordered, g)
		}
	}
	return ordered
}

// mergedAttempts replaces entire variant/scenario/item groups, never individual repeats.
func mergedAttempts(cand, parent *Experiment) ([]*StoredAttempt, error) {
	if parent == nil {
		return cand.Attempts, nil
	}
	if parent.Header.ID == "" || cand.Header.Parent != parent.Header.ID {
		return nil, fmt.Errorf("eval: merge parent does not match candidate parent %q", cand.Header.Parent)
	}
	present := make(map[[3]string]bool)
	result := slices.Clone(cand.Attempts)
	for _, a := range cand.Attempts {
		present[[3]string{a.Variant, a.Scenario, a.Item}] = true
	}
	for _, a := range parent.Attempts {
		if !present[[3]string{a.Variant, a.Scenario, a.Item}] {
			result = append(result, a)
		}
	}
	return result, nil
}

func unmatchedVariant(e *Experiment, variant string) Warning {
	return Warning{Code: "variant_unmatched", Message: fmt.Sprintf("%s variant %s has no counterpart", e.Header.ID, variant)}
}
