package gate

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
)

type namedCriterion struct {
	name   string
	result CriterionResult
}

const (
	receiptVariant = "receipt_only"
	hybridVariant  = "hybrid"
)

// Decide advances a consecutive operational prefix. Completed variants keep
// their exact terminal evidence; incomplete siblings enforce the same-look barrier.
func Decide(e Evaluation, previous []Decision) (Decision, error) {
	return decide(e, previous, Operational)
}
func decide(e Evaluation, previous []Decision, mode DecisionMode) (Decision, error) {
	d := Decision{Look: e.Look, Policy: clonePolicy(e.Policy)}
	if err := validateDecisionInput(e, previous, mode); err != nil {
		return d, err
	}
	current := map[string]VariantResult{}
	for _, v := range e.Variants {
		current[v.Variant] = v
	}
	if err := retainDecidedVariants(&d, e, previous, current); err != nil {
		return d, err
	}
	for _, v := range e.Variants {
		if _, ok := current[v.Variant]; ok {
			d.Variants = append(d.Variants, decideVariant(v, e.Look, e.Policy))
		}
	}
	// Stable policy identity order also preserves an omitted terminal sibling.
	slices.SortFunc(d.Variants, func(a, b VariantDecision) int { return compareVariant(a.Variant, b.Variant) })
	d.Status = Complete
	for _, v := range d.Variants {
		switch v.Status {
		case Incomplete:
			d.Status = Incomplete
			d.Rerun = append(d.Rerun, v.Rerun...)
		case Continue:
			if d.Status != Incomplete {
				d.Status = Continue
			}
		case Pass, Fail, NotPassed, CalibrationIneligible:
		case Inconclusive, Complete, CalibrationEligible:
			return d, fmt.Errorf("gate: invalid variant transition")
		}
	}
	slices.SortFunc(d.Rerun, compareKeys)
	d.Rerun = slices.Compact(d.Rerun)
	return d, nil
}
func decideVariant(v VariantResult, look int, p *Policy) VariantDecision {
	d := VariantDecision{Variant: v.Variant, Look: look, Evidence: cloneVariantResult(v)}
	d.Rerun = slices.Clone(v.Missing)
	for scenario, interval := range v.ScenarioIntervals {
		if interval.Hi < p.Guard {
			d.GuardFailures = append(d.GuardFailures, scenario)
		}
	}
	slices.Sort(d.GuardFailures)
	for _, c := range criteria(v) {
		if c.result.Status == Fail {
			d.FailedCriteria = append(d.FailedCriteria, c.name)
		} else if c.result.Status != Pass {
			d.Inconclusive = append(d.Inconclusive, c.name)
		}
	}
	switch {
	case len(d.Rerun) > 0:
		d.Status = Incomplete
		d.Reason = "rerun missing or infrastructure-failed pairs"
	case len(d.GuardFailures) > 0:
		d.Status = Fail
		d.Reason = "scenario interval guard"
	case len(d.FailedCriteria) > 0:
		d.Status = Fail
		d.Reason = d.FailedCriteria[0]
	case len(d.Inconclusive) == 0:
		d.Status = Pass
	case look < 3:
		d.Status = Continue
	default:
		d.Status = NotPassed
		d.Reason = "precision, not a measured regression, blocked the variant"
	}
	return d
}
func criteria(v VariantResult) []namedCriterion {
	return []namedCriterion{{measureSuccess, v.Success}, {measureUnsupported, v.Unsupported}, {measureEvidence, v.Evidence}, {measureRecall, v.Recall.CriterionResult}}
}
func terminal(s Status) bool {
	return s == Pass || s == Fail || s == NotPassed || s == CalibrationIneligible
}
func validatePrevious(look int, previous []Decision) error {
	if len(previous) == 0 {
		if look != 1 {
			return fmt.Errorf("gate: missing consecutive prefix")
		}
		return nil
	}
	expected := 1
	var last *Decision
	for i := range previous {
		p := &previous[i]
		if p.Look != expected || len(p.Variants) == 0 || p.Policy == nil {
			return fmt.Errorf("gate: malformed consecutive prefix")
		}
		if err := validatePrefixDecision(p); err != nil {
			return err
		}
		if err := validatePrefixTransition(last, p); err != nil {
			return err
		}
		switch {
		case p.Status == Continue:
			expected++
		case p.Status == Incomplete:
		default:
			return fmt.Errorf("gate: work after completed prefix")
		}
		last = p
	}
	if look != expected {
		return fmt.Errorf("gate: out-of-order look or incomplete sibling")
	}
	return nil
}
func schedulingState(variants []VariantDecision) Status {
	state := Complete
	for _, v := range variants {
		if v.Status == Incomplete {
			return Incomplete
		}
		if v.Status == Continue {
			state = Continue
		}
	}
	return state
}
func cloneVariantResult(v VariantResult) VariantResult {
	v.Missing = slices.Clone(v.Missing)
	v.GuardFailures = slices.Clone(v.GuardFailures)
	v.Scenarios = maps.Clone(v.Scenarios)
	v.ScenarioIntervals = maps.Clone(v.ScenarioIntervals)
	return v
}
func cloneVariantDecision(v VariantDecision) VariantDecision {
	v.Rerun = slices.Clone(v.Rerun)
	v.Inconclusive = slices.Clone(v.Inconclusive)
	v.GuardFailures = slices.Clone(v.GuardFailures)
	v.FailedCriteria = slices.Clone(v.FailedCriteria)
	v.Evidence = cloneVariantResult(v.Evidence)
	return v
}
func compareVariant(a, b string) int {
	// Keep the established receipt/hybrid reporting order.
	if a == receiptVariant && b == hybridVariant {
		return -1
	}
	if a == hybridVariant && b == receiptVariant {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
func compareKeys(a, b Key) int {
	for _, pair := range [][2]string{{a.Scenario, b.Scenario}, {a.Item, b.Item}, {a.Variant, b.Variant}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return a.Repeat - b.Repeat
}

// ValidateNextLook checks the complete operational prefix before scheduling work.
// It shares the same prefix validator as Decide and never evaluates new rows.
func ValidateNextLook(policy *Policy, look int, previous []Decision) error {
	if policy == nil || len(policy.Unsupported) < 1 || len(policy.Unsupported) > 2 || look < 1 || look > 3 {
		return fmt.Errorf("gate: invalid next-look policy or index")
	}
	if err := policy.Validate(slices.Sorted(maps.Keys(policy.Unsupported))); err != nil {
		return err
	}
	if err := validatePrevious(look, previous); err != nil {
		return err
	}
	if len(previous) > 0 && !reflect.DeepEqual(policy, previous[len(previous)-1].Policy) {
		return fmt.Errorf("gate: next-look policy changed")
	}
	return nil
}

// validateDecisionInput checks the evaluation and consecutive-prefix contract.
func validateDecisionInput(e Evaluation, previous []Decision, mode DecisionMode) error {
	if mode != Operational && mode != Offline {
		return fmt.Errorf("gate: unknown decision mode %q", mode)
	}
	if e.Look < 1 || e.Look > 3 || len(e.Variants) == 0 || len(e.Variants) > 2 {
		return fmt.Errorf("gate: invalid evaluation")
	}
	names := make([]string, 0, len(e.Variants))
	seen := map[string]bool{}
	for _, v := range e.Variants {
		if v.Variant == "" || seen[v.Variant] {
			return fmt.Errorf("gate: duplicate or empty variant")
		}
		seen[v.Variant] = true
		names = append(names, v.Variant)
	}
	if err := e.Policy.Validate(names); err != nil {
		return err
	}
	if mode == Offline && len(previous) > 0 {
		return fmt.Errorf("gate: offline isolated mode cannot take a prefix")
	}
	if mode == Operational {
		if len(previous) == 0 && len(names) != len(e.Policy.Unsupported) {
			return fmt.Errorf("gate: initial decision omits a policy-declared eligible variant")
		}
		if err := validatePrevious(e.Look, previous); err != nil {
			return err
		}
	}
	return nil
}

// retainDecidedVariants carries immutable terminal and same-look sibling evidence.
func retainDecidedVariants(d *Decision, e Evaluation, previous []Decision, current map[string]VariantResult) error {
	if len(previous) > 0 {
		last := previous[len(previous)-1]
		if !reflect.DeepEqual(last.Policy, e.Policy) {
			return fmt.Errorf("gate: decision policy changed")
		}
		for _, v := range e.Variants {
			if !slices.ContainsFunc(last.Variants, func(old VariantDecision) bool { return old.Variant == v.Variant }) {
				return fmt.Errorf("gate: variant appeared after initial look")
			}
		}
		for _, old := range last.Variants {
			v, present := current[old.Variant]
			if terminal(old.Status) || (last.Look == e.Look && old.Status != Incomplete) {
				if present && !reflect.DeepEqual(v, old.Evidence) {
					return fmt.Errorf("gate: immutable decided evidence changed for %s", old.Variant)
				}
				d.Variants = append(d.Variants, cloneVariantDecision(old))
				delete(current, old.Variant)
			} else if !present {
				return fmt.Errorf("gate: active variant %s omitted", old.Variant)
			}
		}
	}
	return nil
}

// validatePrefixDecision verifies each retained decision against its evidence.
func validatePrefixDecision(p *Decision) error {
	names := make([]string, 0, len(p.Variants))
	seen := map[string]bool{}
	for _, v := range p.Variants {
		if seen[v.Variant] || v.Variant != v.Evidence.Variant || v.Look < 1 || v.Look > p.Look || (!terminal(v.Status) && v.Look != p.Look) {
			return fmt.Errorf("gate: invalid prefix variant identity")
		}
		seen[v.Variant] = true
		names = append(names, v.Variant)
		if !reflect.DeepEqual(v, decideVariant(v.Evidence, v.Look, p.Policy)) {
			return fmt.Errorf("gate: variant decision contradicts retained evidence")
		}
	}
	if len(names) != len(p.Policy.Unsupported) {
		return fmt.Errorf("gate: prefix variant disappeared")
	}
	if err := p.Policy.Validate(names); err != nil {
		return err
	}
	if p.Status != schedulingState(p.Variants) {
		return fmt.Errorf("gate: scheduling state contradicts variants")
	}
	expectedRerun := []Key(nil)
	for _, v := range p.Variants {
		if v.Status == Incomplete {
			expectedRerun = append(expectedRerun, v.Rerun...)
		}
	}
	slices.SortFunc(expectedRerun, compareKeys)
	expectedRerun = slices.Compact(expectedRerun)
	if !slices.Equal(expectedRerun, p.Rerun) {
		return fmt.Errorf("gate: prefix rerun manifest contradicts missing observations")
	}
	return nil
}

// validatePrefixTransition prevents mutation of already decided snapshots.
func validatePrefixTransition(last, p *Decision) error {
	if last != nil {
		if !reflect.DeepEqual(last.Policy, p.Policy) {
			return fmt.Errorf("gate: prefix policy changed")
		}
		for _, old := range last.Variants {
			j := slices.IndexFunc(p.Variants, func(v VariantDecision) bool { return v.Variant == old.Variant })
			if j < 0 {
				return fmt.Errorf("gate: prefix variant disappeared")
			}
			if (terminal(old.Status) || (last.Look == p.Look && old.Status != Incomplete)) && !reflect.DeepEqual(old, p.Variants[j]) {
				return fmt.Errorf("gate: decided prefix snapshot changed")
			}
		}
	}
	return nil
}
