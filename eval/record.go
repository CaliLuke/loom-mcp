package eval

import (
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// AttemptKey identifies an attempt independently of harness-local run IDs.
type AttemptKey struct {
	Variant  string `json:"variant"`
	Scenario string `json:"scenario"`
	Item     string `json:"item"`
	Repeat   int    `json:"repeat"`
}

// StopReason describes why further attempts did not start.
type StopReason string

// AttemptRecord retains quality results and harness failures for one planned key.
type AttemptRecord struct {
	ExecutionKind ExecutionKind
	Prepared      *PreparedReceipt
	Probe         *ProbeStatus
	Artifacts     []StoredArtifact
	AttemptKey
	Index       int
	Outcome     *Outcome
	HarnessErr  error
	NotRun      StopReason
	Started     time.Time
	Duration    time.Duration
	Usage       model.TokenUsage
	Tokens      int
	JudgeUsage  model.TokenUsage
	JudgeTokens int
}

// RunEvidence exports one returned session run including its descendants.
type RunEvidence struct {
	RunID, SessionID, TraceID string
	Export                    runrecord.Export
}

// ScenarioInfo describes selected items and their full dataset's provenance.
type ScenarioInfo struct {
	ID                         string
	Agent                      agent.Ident
	Tags                       []string
	Timeout                    time.Duration
	DatasetSource, DatasetHash string
	Items                      []string
}

// Report retains every planned attempt in declaration order.
type Report struct {
	ExecutionKind                           ExecutionKind
	Variants                                []Variant
	Scenarios                               []ScenarioInfo
	Selection                               Selection
	Repeats, Concurrency, MaxTokens, Tokens int
	Started, Finished                       time.Time
	Stop                                    StopReason
	Attempts                                []*AttemptRecord
	Judge                                   *JudgeInfo
}

// Group counts verdicts for one variant, scenario and item.
type Group struct {
	Variant, Scenario, Item                            string
	Attempts, Passed, Failed, Errored, Pending, NotRun int
	Probes                                             int `json:"probes,omitempty,omitzero"`
	Unassessed                                         int `json:"unassessed,omitempty,omitzero"`
}

const (
	// StopStoreError means durable prepared publication failed.
	StopStoreError StopReason = "store_error"
	// StopCanceled means the caller context stopped new attempts.
	StopCanceled StopReason = "canceled"
	// StopTokenLimit means the token budget was reached.
	StopTokenLimit StopReason = "token_limit"
	// StopLiveModelDisabled means live use was not explicitly enabled.
	StopLiveModelDisabled StopReason = "live_model_disabled"
	// StopCallbackPanic means the attempt observer panicked.
	StopCallbackPanic StopReason = "callback_panic"
)

// String returns the unambiguous variant:scenario:item:repeat identity.
func (k AttemptKey) String() string {
	return fmt.Sprintf("%s:%s:%s:%d", k.Variant, k.Scenario, k.Item, k.Repeat)
}

// Runs returns exports for the completed session prefix, including on harness errors.
func (r *AttemptRecord) Runs() []RunEvidence {
	if r == nil || r.Outcome == nil || r.Outcome.Session == nil {
		return nil
	}
	var evidence []RunEvidence
	for _, result := range r.Outcome.Session.Runs {
		if result != nil && result.Record != nil {
			evidence = append(evidence, RunEvidence{RunID: result.RunID, SessionID: result.SessionID, TraceID: result.TraceID, Export: result.Record.Export()})
		}
	}
	return evidence
}

// Groups returns verdict counts in declaration order without gating the experiment.
func (r *Report) Groups() []Group {
	if r == nil {
		return nil
	}
	var groups []Group
	positions := make(map[[3]string]int)
	for _, a := range r.Attempts {
		key := [3]string{a.Variant, a.Scenario, a.Item}
		pos, exists := positions[key]
		if !exists {
			pos = len(groups)
			positions[key] = pos
			groups = append(groups, Group{Variant: a.Variant, Scenario: a.Scenario, Item: a.Item})
		}
		g := &groups[pos]
		g.Attempts++
		if a.ExecutionKind == ExecutionProbe {
			g.Probes++
		}
		switch a.Verdict() {
		case VerdictUnassessed:
			g.Unassessed++
		case VerdictPassed:
			g.Passed++
		case VerdictFailed:
			g.Failed++
		case VerdictError:
			g.Errored++
		case VerdictPending:
			g.Pending++
		case VerdictNotRun:
			g.NotRun++
		}
	}
	return groups
}

// countUsage reads each recorded invocation once, including descendant and abandoned calls.
func (r *AttemptRecord) countUsage() {
	if r.Outcome.Session == nil {
		return
	}
	for _, result := range r.Outcome.Session.Runs {
		if result == nil || result.Record == nil {
			continue
		}
		for _, run := range result.Record.Runs {
			for _, call := range run.Calls {
				if call.Completion == nil {
					continue
				}
				u := call.Completion.Usage
				r.Usage.InputTokens += u.InputTokens
				r.Usage.OutputTokens += u.OutputTokens
				r.Usage.CacheReadTokens += u.CacheReadTokens
				r.Usage.CacheWriteTokens += u.CacheWriteTokens
				r.Usage.TotalTokens += u.TotalTokens
				tokens := u.TotalTokens
				if tokens <= 0 {
					tokens = u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
				}
				r.Tokens += tokens
			}
		}
	}
}

// stop preserves the first observed reason for stopping new attempts.
func (r *Report) stop(reason StopReason) {
	if r.Stop == "" {
		r.Stop = reason
	}
}
