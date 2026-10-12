package agenttest

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
)

// CaptureHistoryError reports a registered agent's non-nil History policy.
// Capture rejects every such policy because its captured clients cannot be
// inspected, so model calls outside the harness gates cannot be ruled out.
type CaptureHistoryError struct {
	// AgentID identifies the registered agent whose policy prevents capture.
	AgentID agent.Ident
}

// Error explains why capture cannot start safely.
func (e *CaptureHistoryError) Error() string {
	return fmt.Sprintf("agenttest: cannot capture: agent %q has a non-nil History policy; capture cannot rule out an ungated model client", e.AgentID)
}
