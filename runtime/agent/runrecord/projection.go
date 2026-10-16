package runrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// Projection is a sharing artifact, never a replay export. Source hashes refer
	// only to the original export; projected values make no replay-fidelity claim.
	Projection struct {
		Kind       string               `json:"kind"`
		Version    int                  `json:"version"`
		Source     ProjectionSource     `json:"source"`
		Policy     string               `json:"policy"`
		Incomplete bool                 `json:"incomplete"`
		Runs       []ProjectedRun       `json:"runs"`
		Omissions  []ProjectionOmission `json:"omissions"`
		Failures   []ProjectionFailure  `json:"failures"`
	}
	// ProjectionSource identifies the original raw evidence, not projected bytes.
	ProjectionSource struct {
		RootRunID    string `json:"root_run_id"`
		ExportSHA256 string `json:"export_sha256"`
	}
	// ProjectionPolicy explicitly approves, transforms or omits every content leaf.
	// Name should identify the application's exact versioned sharing policy. Content
	// receives owned JSON and must return valid JSON when include is true. No PII
	// detection or authorization is implicit. Identity and structural outcomes are
	// always retained; applications must authorize sharing those before projecting.
	ProjectionPolicy struct {
		Name    string
		Content func(ProjectionContent) (value jsontext.Value, include bool, err error)
	}
	// ProjectionContent is one classified candidate at an unambiguous source path.
	ProjectionContent struct {
		Kind  string
		Path  string
		RunID string
		Role  model.ConversationRole
		Value jsontext.Value
	}
	// ProjectionOmission records an intentionally excluded source value.
	ProjectionOmission struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	// ProjectionFailure reports a failed projection without copying unsafe errors.
	ProjectionFailure struct {
		Path string `json:"path"`
		Code string `json:"code"`
	}
	// ProjectedRun preserves run attribution, outcomes and selected content.
	ProjectedRun struct {
		RunID      string             `json:"run_id"`
		Sealed     bool               `json:"sealed"`
		Transcript []ProjectedMessage `json:"transcript"`
		Calls      []ProjectedCall    `json:"calls"`
		Events     []ProjectedEvent   `json:"events"`
		Content    []ProjectedValue   `json:"content"`
	}
	// ProjectedMessage retains the original message boundary and speaker.
	ProjectedMessage struct {
		Role     model.ConversationRole `json:"role"`
		Parts    []ProjectedValue       `json:"parts"`
		Metadata jsontext.Value         `json:"metadata,omitzero"`
	}
	// ProjectedValue contains policy-approved content, without a raw content hash.
	ProjectedValue struct {
		Kind        string           `json:"kind"`
		Path        string           `json:"path"`
		ToolID      string           `json:"tool_id,omitzero"`
		ToolName    string           `json:"tool_name,omitzero"`
		IsError     bool             `json:"is_error,omitzero"`
		Value       jsontext.Value   `json:"value,omitzero"`
		Attachments []ProjectedValue `json:"attachments,omitzero"`
	}
	// ProjectedCall retains call identity independently of adoption and outcome.
	ProjectedCall struct {
		Key       runstore.CallKey   `json:"key"`
		Class     CallClass          `json:"class"`
		State     runstore.CallState `json:"state"`
		Activity  string             `json:"activity"`
		Step      int                `json:"step"`
		Model     string             `json:"model"`
		Started   time.Time          `json:"started"`
		ErrorKind string             `json:"error_kind,omitzero"`
		Error     jsontext.Value     `json:"error,omitzero"`
		Usage     model.TokenUsage   `json:"usage"`
		LatencyNS int64              `json:"latency_ns"`
		Request   []ProjectedMessage `json:"request"`
		Response  []ProjectedMessage `json:"response"`
		ToolCalls []ProjectedValue   `json:"tool_calls"`
	}
	// ProjectedEvent contains only terminal outcomes or child-run attribution.
	// Other event payloads are explicitly omitted, never copied wholesale.
	ProjectedEvent struct {
		ID           string         `json:"id"`
		Type         string         `json:"type"`
		AgentID      string         `json:"agent_id"`
		SessionID    string         `json:"session_id"`
		TurnID       string         `json:"turn_id"`
		Timestamp    time.Time      `json:"timestamp"`
		Status       string         `json:"status,omitzero"`
		Phase        string         `json:"phase,omitzero"`
		ChildRunID   string         `json:"child_run_id,omitzero"`
		ChildAgentID string         `json:"child_agent_id,omitzero"`
		ToolID       string         `json:"tool_id,omitzero"`
		ToolName     string         `json:"tool_name,omitzero"`
		Diagnostic   jsontext.Value `json:"diagnostic,omitzero"`
	}
	projectionBuilder struct {
		artifact *Projection
		policy   ProjectionPolicy
		runID    string
		role     model.ConversationRole
		errors   []error
	}
)

// FromProjection decodes only the separate sharing schema. It does not restore
// a RunRecord and cannot be passed to a replay API.
func FromProjection(data []byte) (*Projection, error) {
	var p Projection
	if err := json.Unmarshal(data, &p, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode projection: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Project applies an explicit application policy to a fresh sharing DTO. On a
// content failure it returns partial evidence marked Incomplete and a joined
// error. Neither the source record nor any source-owned bytes are mutated.
func (r *RunRecord) Project(policy ProjectionPolicy) (*Projection, error) {
	if r == nil || r.RootRunID == "" || policy.Name == "" || policy.Content == nil {
		return nil, fmt.Errorf("runrecord projection: source and named content policy required")
	}
	raw, err := r.Export().Marshal()
	if err != nil {
		return nil, fmt.Errorf("projection source identity: %w", err)
	}
	digest := sha256.Sum256(raw)
	artifact := &Projection{Kind: "loom-mcp.public-evidence", Version: 1, Source: ProjectionSource{RootRunID: r.RootRunID, ExportSHA256: hex.EncodeToString(digest[:])}, Policy: policy.Name}
	b := &projectionBuilder{artifact: artifact, policy: policy}
	b.omit("content", "raw_evidence")
	for i := range r.Runs {
		b.runID = r.Runs[i].RunID
		artifact.Runs = append(artifact.Runs, b.projectRun(r, &r.Runs[i], fmt.Sprintf("runs/%d", i)))
	}
	artifact.Incomplete = len(artifact.Failures) != 0
	return artifact, errors.Join(b.errors...)
}

// Marshal deterministically encodes a sharing artifact, including its explicit
// Incomplete marker. Consumers must not treat incomplete evidence as complete.
func (p *Projection) Marshal() ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p, json.Deterministic(true))
}

func (p *Projection) validate() error {
	if p == nil || p.Kind != "loom-mcp.public-evidence" || p.Version != 1 || p.Source.RootRunID == "" || p.Policy == "" {
		return fmt.Errorf("runrecord: invalid projection identity, version or policy")
	}
	digest, err := hex.DecodeString(p.Source.ExportSHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("runrecord: invalid projection source digest")
	}
	if p.Incomplete != (len(p.Failures) != 0) {
		return fmt.Errorf("runrecord: inconsistent projection failure marker")
	}
	return nil
}
