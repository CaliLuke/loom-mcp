package engine

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"

// SealRecordActivityCall schedules terminal transcript sealing.
type SealRecordActivityCall struct {
	// Name is the registered agent seal activity name.
	Name string
	// Input fixes the terminal entries and run-log event identity.
	Input *api.SealRecordInput
	// Options overrides registered scheduling options when non-zero.
	Options ActivityOptions
}
