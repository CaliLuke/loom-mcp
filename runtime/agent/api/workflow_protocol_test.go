package api

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowProtocolMismatchError(t *testing.T) {
	for _, got := range []int{0, WorkflowProtocolVersion + 1, WorkflowProtocolVersion - 1} {
		t.Run(fmt.Sprint(got), func(t *testing.T) {
			err := &WorkflowProtocolMismatchError{Want: WorkflowProtocolVersion, Got: got}
			require.ErrorIs(t, fmt.Errorf("workflow: %w", err), ErrWorkflowProtocolMismatch)
			assert.Contains(t, err.Error(), fmt.Sprintf("want %d", WorkflowProtocolVersion))
			assert.Contains(t, err.Error(), fmt.Sprintf("got %d", got))
			assert.Contains(t, err.Error(), "running workflows must be drained before upgrading")
		})
	}
}
