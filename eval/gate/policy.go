package gate

import (
	"fmt"
	"maps"
	"slices"
)

// Policy is the complete versioned rule consumed by evaluation and stopping.
// A proposed value authorizes offline computation only; operational work also
// requires the enclosing verified freeze and consecutive evidence prefix.
type Policy struct {
	Version         string             `json:"version"`
	Success         float64            `json:"success"`
	Unsupported     map[string]float64 `json:"unsupported"`
	Evidence        float64            `json:"evidence"`
	Recall          float64            `json:"recall"`
	Guard           float64            `json:"guard"`
	GuardMethod     string             `json:"guard_method"`
	Alpha           float64            `json:"alpha"`
	Looks           []int              `json:"looks"`
	Repeats         int                `json:"repeats"`
	Bootstrap       Bootstrap          `json:"bootstrap"`
	BootstrapMethod string             `json:"bootstrap_method"`
}

// DecisionMode distinguishes operational prefixes from isolated offline proofs.
type DecisionMode string

const (
	// Operational requires a consecutive prefix and immutable stopped variants.
	Operational DecisionMode = "operational"
	// Offline permits an isolated cumulative look for an explicit dry proof.
	Offline DecisionMode = "offline"
	// Complete means scheduling is finished, not that either variant qualified.
	Complete Status = "COMPLETE"
	// CalibrationEligible identifies an offline candidate meeting measured targets.
	CalibrationEligible Status = "CALIBRATION_ELIGIBLE"
	// CalibrationIneligible excludes an unqualified candidate from live scheduling.
	CalibrationIneligible Status = "CALIBRATION_INELIGIBLE"
)

// VariantDecision retains each variant's immutable stopping evidence and causes.
type VariantDecision struct {
	Variant        string
	Look           int
	Status         Status
	Reason         string
	Rerun          []Key
	Inconclusive   []string
	GuardFailures  []string
	FailedCriteria []string
	Evidence       VariantResult
}

// DecideMode applies a named decision mode. Operational callers use Decide.
func DecideMode(e Evaluation, previous []Decision, mode DecisionMode) (Decision, error) {
	return decide(e, previous, mode)
}

// Validate rejects policy shapes outside the calibrated contract.
func (p *Policy) Validate(variants []string) error {
	if p == nil || p.Version != "per-variant-v2" || p.Success != -.05 || p.Evidence != -.05 || p.Recall != .8 || p.Guard != -.1 || p.Alpha != .05 || p.GuardMethod != "scenario-upper-bound" || p.BootstrapMethod != "stratified-item-percentile" || p.Bootstrap.Resamples != 10000 || p.Repeats != 2 || !slices.Equal(p.Looks, []int{12, 24, 48}) {
		return fmt.Errorf("gate: missing or unsupported decision policy")
	}
	for _, variant := range variants {
		margin, ok := p.Unsupported[variant]
		if !ok || (margin != .02 && margin != .03 && margin != .05) {
			return fmt.Errorf("gate: missing or invalid unsupported margin for %s", variant)
		}
	}
	for variant, margin := range p.Unsupported {
		if variant == "" || (margin != .02 && margin != .03 && margin != .05) {
			return fmt.Errorf("gate: invalid unsupported policy entry")
		}
	}
	return nil
}
func clonePolicy(p *Policy) *Policy {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Looks = slices.Clone(p.Looks)
	copy.Unsupported = maps.Clone(p.Unsupported)
	return &copy
}

// ProposedPolicy constructs the fixed offline rule with explicitly selected
// unsupported margins. It does not authorize live execution or qualification.
func ProposedPolicy(unsupported map[string]float64, seed uint64) Policy {
	return Policy{Version: "per-variant-v2", Success: -.05, Unsupported: maps.Clone(unsupported), Evidence: -.05, Recall: .8, Guard: -.1, GuardMethod: "scenario-upper-bound", Alpha: .05, Looks: []int{12, 24, 48}, Repeats: 2, Bootstrap: Bootstrap{Resamples: 10000, Seed: seed}, BootstrapMethod: "stratified-item-percentile"}
}
