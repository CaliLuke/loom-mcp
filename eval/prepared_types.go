package eval

import "github.com/CaliLuke/loom-mcp/v2/agenttest"

// ExecutionKind identifies evaluation or operational probe execution.
type ExecutionKind string

// EnvironmentIdentity binds an attempt to a host-owned immutable environment.
type EnvironmentIdentity struct {
	Mode       string `json:"mode"`
	SnapshotID string `json:"snapshot_id"`
}

// PreparedBinding identifies a versioned host installation contract.
type PreparedBinding struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// PreparedBuild records the executable provenance of submitted source.
type PreparedBuild struct {
	Compiler         string `json:"compiler"`
	Dependencies     string `json:"dependencies"`
	ExecutableDigest string `json:"executable_digest"`
}

// PreparedReceipt binds frozen content, inputs and environment before registration.
type PreparedReceipt struct {
	CandidateDigest string              `json:"candidate_digest"`
	SurfaceID       string              `json:"surface_id"`
	SurfaceVersion  string              `json:"surface_version"`
	Bindings        []PreparedBinding   `json:"bindings"`
	TaskSnapshot    string              `json:"task_snapshot"`
	SourceSnapshot  string              `json:"source_snapshot"`
	InputSnapshot   string              `json:"input_snapshot"`
	DriverVersion   string              `json:"driver_version,omitempty"`
	ModelConfig     string              `json:"model_config"`
	Environment     EnvironmentIdentity `json:"environment"`
	Build           *PreparedBuild      `json:"build,omitempty"`
}

// ProbeStatus separates execution, delivery, evidence and unassessed quality.
type ProbeStatus struct {
	Execution string `json:"execution"`
	Delivery  string `json:"delivery"`
	Evidence  string `json:"evidence"`
	Quality   string `json:"quality"`
}

// Deliverable declares one confined output expected from the host collector.
type Deliverable struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
}

// CollectedArtifact owns bytes returned by the host; empty Data is a retained empty output.
type CollectedArtifact struct {
	Name      string
	MediaType string
	Data      []byte
}

// StoredArtifact describes verified bytes retained outside the released environment.
type StoredArtifact struct {
	Name        string `json:"name"`
	MediaType   string `json:"media_type"`
	Digest      string `json:"digest"`
	Size        int64  `json:"size"`
	Provisional bool   `json:"provisional,omitempty,omitzero"`
}

// CollectionInput supplies owned receipt and canonical capture to a host collector.
type CollectionInput struct {
	Key           AttemptKey
	Receipt       PreparedReceipt
	Evidence      *agenttest.EvidenceSnapshot
	EvidenceError error
	Provisional   bool
}

type preparedPlan struct {
	Kind      ExecutionKind
	Keys      []AttemptKey
	Scenarios []ScenarioInfo
	Variants  []StoredVariant
	Config    StoredConfig
}

const (
	// ExecutionEvaluation marks an opt-in prepared evaluation.
	ExecutionEvaluation ExecutionKind = "evaluation"
	// ExecutionProbe marks an operational attempt without quality assessment.
	ExecutionProbe ExecutionKind = "probe"
)

const (
	probeExecutionSucceeded  = "succeeded"
	probeExecutionFailed     = "failed"
	probeExecutionCanceled   = "canceled"
	probeExecutionNotRun     = "not_run"
	probeDeliveryNotDeclared = "not_declared"
	probeStatusComplete      = "complete"
	probeStatusIncomplete    = "incomplete"
	probeEvidenceProvisional = "provisional"
	probeQualityNotAssessed  = "not_assessed"
)
