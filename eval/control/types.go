// Package control exposes opt-in scoped candidate, trial and evidence operations.
package control

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

// Service binds candidate, execution and evidence operations to one verified host scope.
type Service struct {
	cfg   Config
	tasks map[string]TaskBinding
	ops   map[Operation]bool
}

// Operation identifies an explicitly enabled control capability.
type Operation string

// Limits sets positive ceilings for bounded model-facing reads and pages.
type Limits struct {
	MaxReadBytes   int
	MaxPageEntries int
}

// TaskBinding pins advertised tasks to immutable host and component installation identities.
type TaskBinding struct {
	Task              Task
	Snapshot          string
	HostBinding       candidate.Binding
	ComponentBindings []candidate.Binding
}

// Config supplies host-owned stores, grants and immutable task bindings.
type Config struct {
	Scope       string
	Role        inspect.Role
	Candidates  *candidate.Store
	Experiments *eval.Store
	Tasks       []TaskBinding
	Executor    Executor
	Evidence    EvidenceProvider
	Operations  []Operation
	Authorize   func(context.Context, Operation) error
	Limits      Limits
}

// EvidenceProvider authorizes a durable job association and returns a fresh exact fixed-role view.
type EvidenceProvider func(context.Context, string) (*inspect.View, error)

// Submission retains normalized host-resolved facts for durable request-key admission.
type Submission struct {
	Scope          string
	RequestKey     string
	Candidate      string
	TaskID         string
	TaskSnapshot   string
	Kind           eval.ExecutionKind
	EvaluationPlan string
	HostBinding    candidate.Binding
}

// JobRecord retains the host-only job identity and evidence association; it is never a public DTO.
type JobRecord struct {
	ID           string
	Submission   Submission
	State        JobState
	ExperimentID string
	Attempt      eval.AttemptKey
	Manifest     inspect.ManifestRef
	Failure      *Error
}

// Executor owns scoped durable acceptance, status, cancellation and restart recovery.
type Executor interface {
	Accept(context.Context, Submission) (JobRecord, error)
	Status(context.Context, string, string) (JobRecord, error) // scope, job ID
	Cancel(context.Context, string, string) (JobRecord, error) // scope, job ID
}

// ToolsetConfig explicitly binds a service to a native toolset name.
type ToolsetConfig struct {
	Name    string
	Service *Service
}

const (
	// DiscoverOperation enables capability discovery.
	DiscoverOperation Operation = "discover"
	// CreateOperation enables draft creation.
	CreateOperation Operation = "create_candidate"
	// GetOperation enables candidate metadata reads.
	GetOperation Operation = "get_candidate"
	// ReadOperation enables bounded candidate reads.
	ReadOperation Operation = "read_candidate"
	// EditOperation enables optimistic candidate edits.
	EditOperation Operation = "edit_candidate"
	// ValidateOperation enables host and core validation.
	ValidateOperation Operation = "validate_candidate"
	// FreezeOperation enables immutable candidate publication.
	FreezeOperation Operation = "freeze_candidate"
	// SubmitOperation enables durable trial submission.
	SubmitOperation Operation = "submit_trial"
	// StatusOperation enables scoped job polling.
	StatusOperation Operation = "get_job"
	// CancelOperation enables host-owned cancellation.
	CancelOperation Operation = "cancel_job"
	// ListEvidenceOperation enables authorized evidence listing.
	ListEvidenceOperation Operation = "list_evidence"
	// ReadEvidenceOperation enables bounded evidence reads.
	ReadEvidenceOperation Operation = "read_evidence"
	// CompareOperation enables safe existing-statistics comparison.
	CompareOperation Operation = "compare_trials"
)
