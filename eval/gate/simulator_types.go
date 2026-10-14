package gate

// Cell identifies one full-factorial synthetic population. DU is bad-oriented;
// every other difference is good-oriented. Kappa couples measures within a run.
type Cell struct{ DY, DU, DE, Delta, Rho, Recall, Kappa float64 }

// Rates are baseline marginal probabilities. Unsupported is bad-oriented.
type Rates struct{ Success, Unsupported, Evidence float64 }

// CalibrationConfig fixes the bootstrap and outcome seeds independently.
// Pilot rates are clamped to [.02,.98]; nil uses labelled assumed defaults.
// Qualifying must give the offline-validated fraction for each variant/scenario.
type CalibrationConfig struct {
	Gate       Config                        `json:"gate"`
	Seed       uint64                        `json:"seed"`
	Workers    int                           `json:"workers"`
	Pilot      map[string]Rates              `json:"pilot"`
	Qualifying map[string]map[string]float64 `json:"qualifying"`
}

// CellReport stores probabilities, Monte Carlo uncertainty and stopping cost.
type CellReport struct {
	ExpectedAttempts float64                       `json:"expected_attempts"`
	Variants         map[string]VariantSimulation  `json:"variants"`
	Cell             Cell                          `json:"cell"`
	Simulations      int                           `json:"simulations"`
	Infeasible       string                        `json:"infeasible"`
	Pass             float64                       `json:"pass"`
	NotPassed        float64                       `json:"not_passed"`
	SE               float64                       `json:"se"`
	ExpectedLooks    float64                       `json:"expected_looks"`
	ExpectedItems    float64                       `json:"expected_items"`
	VariantPass      map[string]float64            `json:"variant_pass"`
	LookPass         [3]float64                    `json:"look_pass"`
	LookFail         [3]float64                    `json:"look_fail"`
	TruncatedPass    [2]float64                    `json:"truncated_pass"`
	Final            map[string]map[Status]float64 `json:"final"`
}

// CalibrationReport retains inputs and all cells in deterministic grid order.
type CalibrationReport struct {
	Version     string                        `json:"version"`
	RateSource  string                        `json:"rate_source"`
	Seed        uint64                        `json:"seed"`
	Baselines   map[string]Rates              `json:"baselines"`
	Qualifying  map[string]map[string]float64 `json:"qualifying"`
	Cells       []CellReport                  `json:"cells"`
	Sensitivity []CellReport                  `json:"sensitivity"`
	Targets     []string                      `json:"targets"`
}

// SimulatorVersion changes whenever synthetic sampling or reporting changes.
const SimulatorVersion = "k19-v2-variant-interval"

// CalibrationGrid returns all 1620 cells in a stable order.
func CalibrationGrid() []Cell {
	out := make([]Cell, 0, 1620)
	for _, y := range []float64{0, -.03, -.05, -.08} {
		for _, u := range []float64{0, .01, .02, .03, .05} {
			for _, d := range []float64{.1, .2, .3} {
				for _, rho := range []float64{.3, .5, .7} {
					for _, e := range []float64{0, -.03, -.05} {
						for _, r := range []float64{.75, .80, .90} {
							out = append(out, Cell{y, u, e, d, rho, r, .5})
						}
					}
				}
			}
		}
	}
	return out
}

// DecisionCell identifies the nine null and 36 single-margin cells.
func (c Cell) DecisionCell() bool {
	return c.axis() != ""
}
func (c Cell) axis() string {
	if c.DY == 0 && c.DU == 0 && c.DE == 0 && c.Recall == .9 {
		return measureNull
	}
	if c.DY == -.05 && c.DU == 0 && c.DE == 0 && c.Recall == .9 {
		return measureSuccess
	}
	if c.DY == 0 && c.DU == .02 && c.DE == 0 && c.Recall == .9 {
		return measureUnsupported
	}
	if c.DY == 0 && c.DU == 0 && c.DE == -.05 && c.Recall == .9 {
		return measureEvidence
	}
	if c.DY == 0 && c.DU == 0 && c.DE == 0 && c.Recall == .8 {
		return measureRecall
	}
	return ""
}

// VariantSimulation records a variant's actual terminal outcomes and joint causes.
type VariantSimulation struct {
	Pass          float64            `json:"pass"`
	NotPassed     float64            `json:"not_passed"`
	SE            float64            `json:"se"`
	LookPass      [3]float64         `json:"look_pass"`
	LookFail      [3]float64         `json:"look_fail"`
	GuardStop     float64            `json:"guard_stop"`
	CriterionStop float64            `json:"criterion_stop"`
	FailureMasks  map[string]float64 `json:"failure_masks"`
}

// CandidateReport preserves the complete result for one proposed unsupported margin.
type CandidateReport struct {
	Margin float64           `json:"margin"`
	Report CalibrationReport `json:"report"`
}

// CandidateSelection reports eligibility separately for each variant.
type CandidateSelection struct {
	Variant       string             `json:"variant"`
	Margin        float64            `json:"margin"`
	Status        Status             `json:"status"`
	WorstNull     float64            `json:"worst_null"`
	WorstBoundary map[string]float64 `json:"worst_boundary"`
}
