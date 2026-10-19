package eval

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

// PreparedAttemptInfo owns resolved item defaults and metadata for preparation.
type PreparedAttemptInfo struct {
	Key     AttemptKey
	Item    Item
	Variant Variant
}

// PrepareAttemptFunc acquires one isolated lease, including partial leases on error.
// Callbacks cooperate with cancellation and must be safe for concurrent attempts.
type PrepareAttemptFunc func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error)

// PreparedDriver executes a fixed driver against its frozen input snapshot.
type PreparedDriver func(context.Context, *agenttest.Harness, Item) (*agenttest.SessionResult, error)

// PreparedAttempt binds installation, inputs and environment lifecycle to one lease.
// Exactly one of Inputs and Drive is required. Every acquired lease needs Release.
// Host callbacks must cooperate with their supplied bounded contexts.
type PreparedAttempt struct {
	BaseOptions  []agenttest.Option
	Harness      AttemptHarnessConfig
	Inputs       []agenttest.Input
	Drive        PreparedDriver
	Receipt      PreparedReceipt
	Deliverables []Deliverable
	Quiesce      func(context.Context) error
	Collect      func(context.Context, CollectionInput) ([]CollectedArtifact, error)
	Release      func(context.Context) error
}
