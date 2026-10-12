// Package evidence classifies deterministic evidence from exported run records.
// It does not invoke a model, judge, harness or workflow.
// Checkpoint section labels embedded in content make original text boundaries
// ambiguous. checkpoint.Sections applies deterministic precedence: first user
// label, last receipt label, then last summary label before that receipt.
// This affects only attribution among user_index, summary and receipt. Sources
// are reported, never gated; presence and gated measures are unaffected.
package evidence

import "regexp"

// EvidenceID is an item-local identity, shared by all of its need points.
type EvidenceID string

// Evidence holds exactly one literal value or lowercase SHA-256 of asset bytes.
// For an asset claim, Token is the materializer-declared grammar token rendered
// in those bytes. It is supported only when the agent receives matching bytes.
type Evidence struct{ Value, Asset, Token string }

// Need identifies the first adopted planner request containing Trigger.
type Need struct {
	Trigger  string
	Evidence []EvidenceID
}

// Spec is materializer-owned truth. Answer tokens must all occur in the final
// answer text and final action arguments considered together. Grammar tokens are
// supported only by agent-received input of an adopted planner request; earlier
// assistant text, tool arguments and stored final outputs do not support them.
type Spec struct {
	Needs    []Need
	Evidence map[EvidenceID]Evidence
	Decoys   []string
	Grammar  *regexp.Regexp
	Answer   []string
}

// Source identifies where a required value appears in an exact request.
type Source string

// NeedResult preserves per-need evidence and the adopted need-point locator.
type NeedResult struct {
	Trigger, RunID   string
	Seq, Attempt     int
	Found            bool
	Present          map[EvidenceID][]Source
	Absent, Recalled []EvidenceID
}

// Result contains reserved evaluation measures and diagnostic sources.
type Result struct {
	NeedFound, AbsentWhenNeeded, Used, UnsupportedClaim, RecallQualifying, RecallSuccess, Exhausted bool
	Compactions, SummaryRejections, OverflowRecoveries, RecallCalls                                 int
	Sources                                                                                         []Source
	Needs                                                                                           []NeedResult
}

const (
	// Pins precede the checkpoint message.
	Pins Source = "pins"
	// UserIndex is verbatim checkpoint user text or retained attachments.
	UserIndex Source = "user_index"
	// Summary is model-generated checkpoint text.
	Summary Source = "summary"
	// Receipt is the framework checkpoint receipt.
	Receipt Source = "receipt"
	// Tail is all remaining request content.
	Tail Source = "tail"
	// Recall is a result correlated with a recall tool use.
	Recall Source = "recall"
)
