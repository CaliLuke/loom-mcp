package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
)

// validateModelCallOverride preserves nil inheritance while rejecting explicit
// unlimited or negative per-run overrides before model work can be scheduled.
func validateModelCallOverride(overrides *PolicyOverrides) error {
	if overrides != nil && overrides.MaxModelCalls != nil && *overrides.MaxModelCalls <= 0 {
		return fmt.Errorf("%w: MaxModelCalls override must be positive", ErrInvalidConfig)
	}
	return nil
}

// recordModelCallPolicy snapshots the owning worker's policy in the same
// activity result as the run's initial content or continuation transaction.
func (r *Runtime) recordModelCallPolicy(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	request := input.ModelCallPolicy
	if err := validateModelCallOverride(&PolicyOverrides{MaxModelCalls: request.MaxModelCalls}); err != nil {
		return nil, err
	}
	reg, ok := r.agentByID(request.AgentID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrAgentNotFound, request.AgentID)
	}
	cap := reg.Policy.MaxModelCalls
	if cap < 0 {
		return nil, fmt.Errorf("%w: MaxModelCalls must not be negative", ErrInvalidConfig)
	}
	if request.MaxModelCalls != nil {
		cap = *request.MaxModelCalls
	}
	content := *input
	content.ModelCallPolicy = nil
	out, err := r.RecordContentActivity(ctx, &content)
	if err != nil {
		return nil, err
	}
	out.MaxModelCalls = new(cap)
	return out, nil
}
