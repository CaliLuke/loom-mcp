package runtime

import (
	"context"
	"fmt"
	"maps"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

type (
	// ContextRecovery is the stamped lookup configuration. K14 adds overflow controls.
	ContextRecovery = api.ContextRecovery
	// RecallAccess identifies one authorized run whose evidence a lookup enters.
	RecallAccess        = recall.Access
	recoverySettingsKey struct{}
)

// WithContextRecovery configures context lookup for an agent independently of compaction.
func WithContextRecovery(id agent.Ident, settings ContextRecovery) RuntimeOption {
	return func(opts *Options) {
		if opts.ContextRecovery == nil {
			opts.ContextRecovery = map[agent.Ident]ContextRecovery{}
		}
		opts.ContextRecovery[id] = settings
	}
}

// WithRecallAuthorizer authorizes each run entered by a lookup, including its current run.
// Its errors become recall_unauthorized without exposing the application's reason.
func WithRecallAuthorizer(authorize func(context.Context, RecallAccess) error) RuntimeOption {
	return func(opts *Options) {
		opts.RecallAuthorizer = authorize
	}
}

func cloneRecoveryOptions(settings map[agent.Ident]ContextRecovery) map[agent.Ident]ContextRecovery {
	for id := range settings {
		if id == "" {
			panic(fmt.Sprintf("runtime: invalid context recovery agent %q", id))
		}
	}
	return maps.Clone(settings)
}
