package control

import (
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

// Task advertises task identity and explicitly supported execution modes.
type Task struct {
	ID              string               `json:"id"`
	Modes           []eval.ExecutionKind `json:"modes"`
	EvaluationPlans []string             `json:"evaluation_plans,omitempty"`
}

// Discovery contains authorized capabilities without hidden execution configuration.
type Discovery struct {
	Surface    *candidate.Discovery `json:"surface,omitempty"`
	Tasks      []Task               `json:"tasks,omitempty"`
	Operations []Operation          `json:"operations"`
}

// CreateRequest selects exactly one authorized baseline or frozen source.
type CreateRequest struct {
	Baseline        string `json:"baseline,omitempty"`
	SourceCandidate string `json:"source_candidate,omitempty"`
}

// GetCandidateRequest selects one draft or frozen candidate for metadata.
type GetCandidateRequest struct {
	DraftID   string `json:"draft_id,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

// CandidateInfo describes component identities without copying their contents.
type CandidateInfo struct {
	DraftID    string                   `json:"draft_id,omitempty"`
	Revision   uint64                   `json:"revision,omitempty"`
	Candidate  string                   `json:"candidate,omitempty"`
	Components []candidate.ComponentRef `json:"components"`
}

// ReadCandidateRequest selects exact bounded bytes from a frozen candidate or draft revision.
type ReadCandidateRequest struct {
	DraftID          string `json:"draft_id,omitempty"`
	ExpectedRevision uint64 `json:"expected_revision,omitempty"`
	Candidate        string `json:"candidate,omitempty"`
	Component        string `json:"component"`
	Offset           int64  `json:"offset,omitempty"`
	MaxBytes         int    `json:"max_bytes,omitempty"`
}

// CandidateRead returns owned bytes with digest, byte offsets and continuation.
type CandidateRead struct {
	Data       []byte `json:"data,omitempty"`
	MediaType  string `json:"media_type"`
	Digest     string `json:"digest"`
	Offset     int64  `json:"offset"`
	Total      int64  `json:"total"`
	NextOffset *int64 `json:"next_offset,omitempty"`
	Truncated  bool   `json:"truncated"`
}

// EditRequest submits one optimistic component mutation with text or base64 bytes.
type EditRequest struct {
	DraftID          string              `json:"draft_id"`
	ExpectedRevision uint64              `json:"expected_revision"`
	Operation        candidate.Operation `json:"operation"`
	Name             string              `json:"name"`
	Slot             string              `json:"slot,omitempty"`
	MediaType        string              `json:"media_type,omitempty"`
	Content          *string             `json:"content,omitempty"`
	ContentBase64    *string             `json:"content_base64,omitempty"`
	Dependencies     []string            `json:"dependencies,omitempty"`
}

// RevisionRequest pins a draft operation to an expected optimistic revision.
type RevisionRequest struct {
	DraftID          string `json:"draft_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

// Validation confirms validation succeeded; failures return a typed Error.
type Validation struct {
	Valid bool `json:"valid"`
}

// Frozen identifies a retained immutable candidate and its canonical manifest.
type Frozen struct {
	Candidate string             `json:"candidate"`
	Manifest  candidate.Manifest `json:"manifest"`
}

// SubmitRequest selects a frozen candidate and host task under an idempotency key.
type SubmitRequest struct {
	RequestKey     string             `json:"request_key"`
	Candidate      string             `json:"candidate"`
	TaskID         string             `json:"task_id"`
	Kind           eval.ExecutionKind `json:"kind,omitempty"`
	EvaluationPlan string             `json:"evaluation_plan,omitempty"`
}

// JobRequest selects one opaque job handle in the host scope.
type JobRequest struct {
	JobID string `json:"job_id"`
}

// JobState describes host-owned execution independently of probe quality.
type JobState string

// Job projects only opaque identity, status and safe evidence availability.
type Job struct {
	ID                string   `json:"id"`
	State             JobState `json:"state"`
	EvidenceAvailable bool     `json:"evidence_available"`
	Failure           *Error   `json:"failure,omitempty"`
}

// EvidenceListRequest pages the exact role-bound evidence view of one job.
type EvidenceListRequest struct {
	JobID  string `json:"job_id"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// EvidenceReadRequest reads one locator within the exact job evidence view.
type EvidenceReadRequest struct {
	JobID    string `json:"job_id"`
	Locator  string `json:"locator"`
	Offset   int64  `json:"offset,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// CompareRequest selects two authorized exact trial associations.
type CompareRequest struct {
	BaselineJobID  string `json:"baseline_job_id"`
	CandidateJobID string `json:"candidate_job_id"`
}

// ComparisonSide projects existing aggregate execution and optional quality statistics.
type ComparisonSide struct {
	Attempts            int            `json:"attempts"`
	Ran                 int            `json:"ran"`
	MedianTokens        int            `json:"median_tokens"`
	MedianDurationNanos int64          `json:"median_duration_nanos"`
	Quality             *QualityCounts `json:"quality,omitempty"`
}

// QualityCounts retains evaluated verdict counts without check or judge content.
type QualityCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errored int `json:"errored"`
	Pending int `json:"pending"`
	NotRun  int `json:"not_run"`
}

// Comparison compares existing statistics without inferring probe quality or a winner.
type Comparison struct {
	Status       string         `json:"status"` // comparable or not_comparable for QUALITY
	Baseline     ComparisonSide `json:"baseline"`
	Candidate    ComparisonSide `json:"candidate"`
	WarningCodes []string       `json:"warning_codes,omitempty"`
}

const (
	// Queued means the host durably accepted work that has not started.
	Queued JobState = "queued"
	// Running means the host owns started execution.
	Running JobState = "running"
	// CancelRequested records cancellation intent before stoppedness is established.
	CancelRequested JobState = "cancel_requested"
	// Succeeded means execution and required finalization completed.
	Succeeded JobState = "succeeded"
	// Failed means a recorded execution or infrastructure failure is terminal.
	Failed JobState = "failed"
	// Canceled means execution stopped and cancellation finalized safely.
	Canceled JobState = "canceled"
	// Interrupted means execution ownership or effects are uncertain and cannot be replayed.
	Interrupted JobState = "interrupted"
)
