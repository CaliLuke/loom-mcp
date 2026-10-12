package gate

import "fmt"

type namedCriterion struct {
	name   string
	result CriterionResult
}

// Decide applies the shared stopping rule. Previous decisions, when supplied,
// must form a consecutive CONTINUE prefix. An INCOMPLETE look is rerun, not
// advanced. Callers may decide an isolated cumulative look with no prefix.
func Decide(e Evaluation, previous []Decision) (Decision, error) {
	d := Decision{Look: e.Look}
	if e.Look < 1 || e.Look > 3 || len(e.Variants) != 2 {
		return d, fmt.Errorf("gate: invalid evaluation")
	}
	if err := validatePrevious(e.Look, previous); err != nil {
		return d, err
	}
	if len(e.Missing) > 0 {
		d.Status = Incomplete
		d.Reason = "rerun missing or infrastructure-failed runs via Parent"
		d.Rerun = append([]Key(nil), e.Missing...)
		return d, nil
	}
	if len(e.GuardFailures) > 0 {
		d.Status = Fail
		d.Reason = fmt.Sprintf("scenario success guard failed: %v", e.GuardFailures)
		return d, nil
	}
	for _, v := range e.Variants {
		for _, c := range criteria(v) {
			if c.result.Status == Fail {
				d.Status = Fail
				d.Reason = v.Variant + "/" + c.name
				return d, nil
			}
			if c.result.Status != Pass {
				d.Inconclusive = append(d.Inconclusive, v.Variant+"/"+c.name)
			}
		}
	}
	switch {
	case len(d.Inconclusive) == 0:
		d.Status = Pass
	case e.Look < 3:
		d.Status = Continue
	default:
		d.Status = NotPassed
		d.Reason = "precision, not a measured regression, blocked the gate"
	}
	return d, nil
}

func criteria(v VariantResult) []namedCriterion {
	return []namedCriterion{{measureSuccess, v.Success}, {measureUnsupported, v.Unsupported}, {measureEvidence, v.Evidence}, {measureRecall, v.Recall.CriterionResult}}
}

func validatePrevious(look int, previous []Decision) error {
	for i, p := range previous {
		if p.Look != i+1 || p.Status != Continue {
			return fmt.Errorf("gate: later look refused after %s at look %d", p.Status, p.Look)
		}
	}
	if len(previous) > 0 && look != len(previous)+1 {
		return fmt.Errorf("gate: out-of-order look")
	}
	return nil
}
