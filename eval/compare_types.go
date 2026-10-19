package eval

// Side selects one experiment variant; empty Variant selects its only variant.
type Side struct {
	Experiment *Experiment
	Variant    string
}

// CompareOptions optionally overlays a candidate rerun on its recorded parent.
type CompareOptions struct{ Parent *Experiment }

// SideInfo identifies an experiment side without retaining its stored exports.
type SideInfo struct {
	ExecutionKind ExecutionKind `json:"execution_kind,omitempty"`
	Version       int           `json:"version,omitempty,omitzero"`
	ID            string        `json:"id"`
	Label         string        `json:"label"`
	Variant       string        `json:"variant"`
	Status        string        `json:"status"`
	Complete      bool          `json:"complete"`
}

// Warning qualifies a comparison using a stable machine-readable code.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Pair identifies a scenario item independently of variant and repeat.
type Pair struct {
	Scenario string `json:"scenario"`
	Item     string `json:"item"`
}

// Comparison contains intersection-only deltas in candidate declaration order.
type Comparison struct {
	Baseline      SideInfo     `json:"baseline"`
	Candidate     SideInfo     `json:"candidate"`
	Warnings      []Warning    `json:"warnings"`
	OnlyBaseline  []Pair       `json:"only_baseline"`
	OnlyCandidate []Pair       `json:"only_candidate"`
	Groups        []GroupDelta `json:"groups"`
	Totals        Totals       `json:"totals"`
}

// Totals aggregates only the compared groups on each side.
type Totals struct {
	Baseline  GroupStats `json:"baseline"`
	Candidate GroupStats `json:"candidate"`
}

// GroupDelta compares all repeats of one shared scenario item.
type GroupDelta struct {
	Scenario     string       `json:"scenario"`
	Item         string       `json:"item"`
	Status       string       `json:"status"`
	Uncalibrated bool         `json:"uncalibrated"`
	Truncated    bool         `json:"truncated"`
	Baseline     GroupStats   `json:"baseline"`
	Candidate    GroupStats   `json:"candidate"`
	Checks       []CheckDelta `json:"checks"`
	Claims       []ClaimDelta `json:"claims"`
	Scores       []ScoreDelta `json:"scores"`
	Tools        *ToolDelta   `json:"tools,omitempty"`
	// Judgment counts qualify a group even when no claim fraction changed.
	Judgments          int `json:"judgments"`
	TruncatedJudgments int `json:"truncated_judgments"`
}

// CheckCounts records the passed fraction of observed checks.
type CheckCounts struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

// CheckDelta reports a changed check fraction and a failing sample diagnostic.
type CheckDelta struct {
	Name       string      `json:"name"`
	Baseline   CheckCounts `json:"baseline"`
	Candidate  CheckCounts `json:"candidate"`
	Diagnostic string      `json:"diagnostic"`
}

// ClaimCounts includes every judged unit, including errors, in Total.
type ClaimCounts struct {
	Entailed     int `json:"entailed"`
	Total        int `json:"total"`
	Errors       int `json:"errors"`
	Uncalibrated int `json:"uncalibrated"`
	Truncated    int `json:"truncated"`
}

// ClaimDelta pools judgments by claim, subject and declared tool selector.
type ClaimDelta struct {
	ClaimID      string      `json:"claim_id"`
	Subject      string      `json:"subject"`
	Tool         string      `json:"tool"`
	Baseline     ClaimCounts `json:"baseline"`
	Candidate    ClaimCounts `json:"candidate"`
	Uncalibrated bool        `json:"uncalibrated"`
	Truncated    bool        `json:"truncated"`
	Rationale    string      `json:"rationale"`
}

// ScoreDelta compares medians; nil denotes a score absent on that side.
// Threshold is the candidate's threshold, or the baseline's if absent there.
type ScoreDelta struct {
	Name      string   `json:"name"`
	Baseline  *float64 `json:"baseline"`
	Candidate *float64 `json:"candidate"`
	Threshold *float64 `json:"threshold,omitempty"`
}
