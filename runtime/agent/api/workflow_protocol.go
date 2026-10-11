package api

import (
	"errors"
	"fmt"
)

// WorkflowProtocolMismatchError reports an input recorded for a different agent
// workflow protocol. Running workflows must be drained before upgrading.
type WorkflowProtocolMismatchError struct {
	// Want is the protocol version required by this worker.
	Want int
	// Got is the protocol version stamped into the workflow input.
	Got int
}

// WorkflowProtocolVersion is the version of the agent workflow's inputs, outputs,
// signals and state. Every change to workflow code must bump it. Running workflows
// must be drained before deploying a new version.
const WorkflowProtocolVersion = 13

// ErrWorkflowProtocolMismatch matches WorkflowProtocolMismatchError via errors.Is.
var ErrWorkflowProtocolMismatch = errors.New("workflow protocol mismatch")

// Error describes both versions and the required upgrade procedure.
func (e *WorkflowProtocolMismatchError) Error() string {
	return fmt.Sprintf("workflow protocol mismatch: want %d, got %d; running workflows must be drained before upgrading", e.Want, e.Got)
}

// Is classifies this error as ErrWorkflowProtocolMismatch.
func (e *WorkflowProtocolMismatchError) Is(target error) bool {
	return target == ErrWorkflowProtocolMismatch
}
