package policy

import (
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// ModelCallLimit records an invocation reservation rejected before model middleware.
// Call identifies a reserved sequence, not a started provider call.
type ModelCallLimit struct {
	// Limit is the positive configured reservation allowance.
	Limit int `json:"limit"`
	// Call is the unsent reservation that exceeded Limit.
	Call runstore.CallKey `json:"call"`
}

// ModelCallLimitReason is the stable admission refusal classifier.
const ModelCallLimitReason = "model_call_cap"

// ErrModelCallLimit matches a model invocation rejected by the run cap.
var ErrModelCallLimit = errors.New("model call cap exceeded")

// Error describes the exhausted reservation allowance.
func (e *ModelCallLimit) Error() string {
	return fmt.Sprintf("model call cap %d exceeded by reservation %d", e.Limit, e.Call.Seq)
}

// Unwrap exposes the stable refusal sentinel for bounded error classification.
func (e *ModelCallLimit) Unwrap() error {
	return ErrModelCallLimit
}

// Validate rejects evidence that cannot identify a refused positive reservation.
func (e *ModelCallLimit) Validate() error {
	if e == nil || e.Limit <= 0 || e.Call.RunID == "" || e.Call.Attempt < 1 || e.Call.Seq <= e.Limit {
		return errors.New("invalid model call limit evidence")
	}
	return nil
}
