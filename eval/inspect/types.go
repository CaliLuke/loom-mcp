// Package inspect publishes immutable, authorized experiment evidence.
package inspect

import (
	"encoding/json/jsontext"
	"errors"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// Workspace retains immutable evidence while reusing candidate and experiment stores.
type Workspace struct {
	mu          sync.Mutex
	dir         string
	experiments *eval.Store
	candidates  *candidate.Store
	limits      Limits
}

// View binds a finite manifest scope to one host-authorized role and policy.
type View struct {
	workspace *Workspace
	refs      []ManifestRef
	policy    ViewPolicy
}

// ManifestRef is an immutable semantic manifest SHA256 handle.
type ManifestRef string

// Role identifies a host-selected evidence audience.
type Role string

// Kind identifies a structurally authorized evidence category.
type Kind string

// State distinguishes retained, missing, withheld and provisional content.
type State string

// Limits sets positive host ceilings; no field supports unlimited access.
type Limits struct {
	MaxArtifactBytes int64
	MaxEntries       int
	MaxPageEntries   int
	MaxReadBytes     int
}

// Origin binds evidence to one complete experiment attempt identity.
type Origin struct {
	ExperimentID string
	Key          eval.AttemptKey
}

// Attachment submits owned host bytes with explicit audience grants.
type Attachment struct {
	Name       string
	Kind       Kind // TaskSource, DecisionNote, EvaluatorReference only
	MediaType  string
	Data       []byte
	Audiences  []Role
	Provenance string
}

// Rendering submits host-rendered bytes tied to an exact source digest and renderer version.
type Rendering struct {
	Name            string
	SourceKind      Kind // TaskSource, Deliverable or the two component kinds
	SourceName      string
	SourceDigest    string // exact raw source SHA256; verified before publication
	MediaType       string
	Data            []byte
	RendererVersion string
	Audiences       []Role
}

// ArtifactContent supplies owned artifact bytes to a publication-time host redactor.
type ArtifactContent struct {
	Role         Role
	Kind         Kind
	Name         string
	MediaType    string
	SourceDigest string
	Data         []byte
}

// ArtifactRedactor approves, transforms or withholds artifact data without changing scope.
type ArtifactRedactor func(ArtifactContent) (data []byte, include bool, err error)

// Policy materializes one named role projection using host-owned callbacks.
type Policy struct {
	Role            Role
	Name            string // nonblank exact versioned policy identity
	RunContent      func(runrecord.ProjectionContent) (jsontext.Value, bool, error)
	ArtifactContent ArtifactRedactor
}

// Publication retains exact source evidence and materializes immutable role views.
type Publication struct {
	Origin               Origin
	BaselineDigest       string
	CandidateDigest      string
	ExpectedDeliverables []eval.Deliverable
	Attachments          []Attachment
	Renderings           []Rendering
	Policies             []Policy // unique roles; each materializes immutable role bytes
	// Provisional means bytes were retained without confirmed quiescence.
	Provisional bool // explicit host truth for capture-only/unfinalized evidence
}

// Scope names a finite set of immutable manifests, without wildcard expansion.
type Scope struct{ Manifests []ManifestRef }

// ViewPolicy rebinds a previously published role and exact policy identity.
type ViewPolicy struct {
	Role Role
	Name string // must match role policy materialized in every scoped manifest
}

// ListQuery pages rows within an already authorized view.
type ListQuery struct {
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}

// ReadQuery pages exact bytes from an authorized opaque locator.
type ReadQuery struct {
	Locator  string `json:"locator"`
	Offset   int64  `json:"offset,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// Provenance attributes evidence for editor and evaluator views only.
type Provenance struct {
	ExperimentID    string           `json:"experiment_id,omitempty"`
	Key             *eval.AttemptKey `json:"attempt,omitempty"`
	RunID           string           `json:"run_id,omitempty"`
	Step            int              `json:"step,omitempty"`
	CallSeq         int              `json:"call_seq,omitempty"`
	CallAttempt     int              `json:"call_attempt,omitempty"`
	CandidateDigest string           `json:"candidate_digest,omitempty"`
	BaselineDigest  string           `json:"baseline_digest,omitempty"`
	Note            string           `json:"note,omitempty"`
}

// Entry describes one role-approved artifact; missing sizes and digests remain absent.
type Entry struct {
	Locator         string      `json:"locator"`
	Kind            Kind        `json:"kind"`
	Label           string      `json:"label"`
	MediaType       string      `json:"media_type,omitempty"`
	State           State       `json:"state"`
	Size            *int64      `json:"size,omitempty"`          // pointer preserves available zero bytes
	Digest          string      `json:"digest,omitempty"`        // SHA256 of RETURNED full body
	SourceDigest    string      `json:"source_digest,omitempty"` // original source, editor/evaluator only
	RendererVersion string      `json:"renderer_version,omitempty"`
	SourceLocator   string      `json:"source_locator,omitempty"`
	Links           []string    `json:"links,omitempty"` // authorized view-local locators only
	Provenance      *Provenance `json:"provenance,omitempty"`
	Incomplete      bool        `json:"incomplete,omitempty"`
	Code            string      `json:"code,omitempty"` // safe fixed diagnostic, never OS/callback text
}

// Page returns bounded rows and an explicit continuation offset.
type Page struct {
	Entries    []Entry `json:"entries"`
	Total      int     `json:"total"`
	NextOffset *int    `json:"next_offset,omitempty"`
	Truncated  bool    `json:"truncated"`
}

// ReadResult returns bounded owned bytes; nil Total means missing or withheld content.
type ReadResult struct {
	Entry      Entry  `json:"entry"`
	Data       []byte `json:"data,omitempty"` // JSON base64; Go caller receives bytes
	Offset     int64  `json:"offset"`
	Total      *int64 `json:"total,omitempty"`
	NextOffset *int64 `json:"next_offset,omitempty"`
	Truncated  bool   `json:"truncated"`
}

// ComponentChange links a deterministic component change to its exact before and after contents.
type ComponentChange struct {
	Name          string `json:"name"`
	Operation     string `json:"operation"` // add/delete/modify
	BeforeDigest  string `json:"before_digest,omitempty"`
	AfterDigest   string `json:"after_digest,omitempty"`
	BeforeLocator string `json:"before_locator,omitempty"`
	AfterLocator  string `json:"after_locator,omitempty"`
}

const (
	// Solver permits only explicitly granted task inputs.
	Solver Role = "solver"
	// Editor permits authorized sources, components, observations and notes.
	Editor Role = "editor"
	// Selector permits sources and outputs under opaque labels.
	Selector Role = "selector"
	// Evaluator permits authorized observations and explicit reference attachments.
	Evaluator Role = "evaluator"
)

const (
	// TaskSource identifies a host-retained task attachment.
	TaskSource Kind = "task_source"
	// BaselineComponent identifies immutable baseline bytes.
	BaselineComponent Kind = "baseline_component"
	// CandidateComponent identifies immutable candidate bytes.
	CandidateComponent Kind = "candidate_component"
	// ComponentDiff identifies deterministic structured component changes.
	ComponentDiff Kind = "component_diff"
	// RunView identifies an approved canonical root-run projection.
	RunView Kind = "run_view"
	// CallView identifies one approved canonical model call.
	CallView Kind = "call_view"
	// Deliverable identifies declared retained output bytes.
	Deliverable Kind = "deliverable"
	// DecisionNote identifies opaque application-owned decision data.
	DecisionNote Kind = "decision_note"
	// EvaluatorReference identifies structurally evaluator-only reference data.
	EvaluatorReference Kind = "evaluator_reference"
	// Rendered identifies a versioned host rendering of an exact source.
	Rendered Kind = "rendered"
)

const (
	// Available means exact retained bytes are readable.
	Available State = "available"
	// Missing means evidence is unavailable; empty content is separate.
	Missing State = "missing"
	// Withheld means a host-granted entry was intentionally omitted.
	Withheld State = "withheld"
	// Provisional means bytes were retained without confirmed quiescence.
	Provisional State = "provisional"
)

var (
	// ErrNotFound does not disclose unknown or unauthorized identities.
	ErrNotFound = errors.New("inspect: not found")
	// ErrInvalidInput reports invalid host configuration or read bounds.
	ErrInvalidInput = errors.New("inspect: invalid input")
	// ErrIntegrity reports evidence whose persisted identity cannot be verified.
	ErrIntegrity = errors.New("inspect: integrity failure")
)

// DefaultLimits returns 16 MiB per body, 4096 entries, 100 rows and 64 KiB per read.
func DefaultLimits() Limits {
	return Limits{16 << 20, 4096, 100, 64 << 10}
}
