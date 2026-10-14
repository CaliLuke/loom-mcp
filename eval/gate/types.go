// Package gate implements paired release criteria without model or harness I/O.
package gate

// Key identifies one planned run. Repeat is one-based.
type Key struct {
	Variant  string `json:"variant"`
	Scenario string `json:"scenario"`
	Item     string `json:"item"`
	Repeat   int    `json:"repeat"`
}

// Row contains binary observations. Infrastructure removes a run from pairing;
// RunFailed remains an outcome and forces Success false.
type Row struct {
	Key
	Success, Unsupported, Evidence bool
	Qualifying, Recalled           bool
	Infrastructure, RunFailed      bool
}

// Table contains observations; duplicate keys are errors (overlay reruns first).
type Table []Row

// Config identifies the gated populations. Items, when supplied, is the complete
// cumulative manifest; without it, the union of observed items defines the plan.
// Policy is required and owns alpha, bootstrap identity and decision margins.
type Config struct {
	Policy              *Policy
	Baseline            string
	Variants, Scenarios []string
	Items               map[string][]string
	Repeats             int
}

// Interval is a point estimate and a two-sided confidence interval.
type Interval struct{ Estimate, Lo, Hi float64 }

// CriterionResult records a decision and its precision bounds.
type CriterionResult struct {
	Interval
	Status Status
	Reason string
}

// RecallResult includes the item-cluster design effect used by Wilson.
type RecallResult struct {
	CriterionResult
	N                     int
	Rho, Deff, EffectiveN float64
}

// VariantResult holds all four criteria and scenario success differences.
type VariantResult struct {
	Missing                        []Key
	GuardFailures                  []string
	ScenarioIntervals              map[string]Interval
	Variant                        string
	Success, Unsupported, Evidence CriterionResult
	Recall                         RecallResult
	Scenarios                      map[string]float64
}

// Evaluation contains one cumulative look's estimates and missing run keys.
type Evaluation struct {
	Policy        *Policy
	Baseline      string
	Look          int
	Variants      []VariantResult
	Missing       []Key
	GuardFailures []string
}

// Status is a criterion or whole-gate state; inconclusive never implies pass.
type Status string

// Decision is the stopping result for a cumulative look.
type Decision struct {
	Policy       *Policy
	Variants     []VariantDecision
	Look         int
	Status       Status
	Reason       string
	Rerun        []Key
	Inconclusive []string
}

const (
	// Pass certifies all required bounds.
	Pass Status = "PASS"
	// Fail identifies a measured regression or guard violation.
	Fail Status = "FAIL"
	// Inconclusive means an interval crosses or touches a margin.
	Inconclusive Status = "INCONCLUSIVE"
	// Continue requests the next held-out partition.
	Continue Status = "CONTINUE"
	// NotPassed means precision prevented certification at the final look.
	NotPassed Status = "NOT_PASSED"
	// Incomplete requires infrastructure reruns before a decision.
	Incomplete Status = "INCOMPLETE"
)

const (
	measureNull        = "null"
	measureSuccess     = "success"
	measureUnsupported = "unsupported"
	measureEvidence    = "evidence"
	measureRecall      = "recall"
)
