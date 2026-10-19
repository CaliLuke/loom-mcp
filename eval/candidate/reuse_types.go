package candidate

import "errors"

// ComponentOrigin identifies the immediate frozen source of a current component.
type ComponentOrigin struct {
	// Name is unchanged from the selected source component.
	Name string `json:"name"`
	// SourceCandidate is the immediate source manifest SHA-256.
	SourceCandidate string `json:"source_candidate"`
	// SourceDigest is the exact component byte SHA-256.
	SourceDigest string `json:"source_digest"`
}

// PreviewCopyRequest selects roots whose full source dependency closure is previewed.
type PreviewCopyRequest struct {
	SourceCandidate string   `json:"source_candidate"`
	Components      []string `json:"components"`
}

// CopyPlan describes the complete proposed import before any draft mutation.
// Digest confirms all fields, including destination content and collision names.
type CopyPlan struct {
	Digest   string `json:"digest"`
	Version  int    `json:"version"`
	DraftID  string `json:"draft_id"`
	Revision uint64 `json:"revision"`
	// DestinationDigest binds complete destination content and existing origins.
	DestinationDigest string   `json:"destination_digest"`
	SurfaceDigest     string   `json:"surface_digest"`
	BaselineDigest    string   `json:"baseline_digest"`
	SourceCandidate   string   `json:"source_candidate"`
	Selected          []string `json:"selected"`
	// Closure includes selected roots and every transitive source dependency.
	Closure []ComponentRef `json:"closure"`
	// Replace lists every existing destination name that would be overwritten.
	Replace []string `json:"replace"`
}

// CopyRequest explicitly confirms one preview's complete closure and replacements.
type CopyRequest struct {
	SourceCandidate string   `json:"source_candidate"`
	Components      []string `json:"components"`
	Replace         []string `json:"replace"`
	PlanDigest      string   `json:"plan_digest"`
}

// CopyResult retains the applied preview identity and the updated owned draft.
type CopyResult struct {
	Draft      Draft    `json:"draft"`
	PlanDigest string   `json:"plan_digest"`
	Selected   []string `json:"selected"`
	Closure    []string `json:"closure"`
}

// ErrCopyPlan requires a fresh preview when copy confirmation does not match.
var ErrCopyPlan = errors.New("candidate: copy plan does not match; preview again")
