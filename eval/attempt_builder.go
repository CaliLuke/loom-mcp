package eval

import (
	"context"
	"errors"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

// AttemptHarnessConfig declares application setup for one isolated attempt.
// Register is required. Options configure models and other application settings;
// Setup optionally installs fixtures after registration. Runner-owned options are
// applied by NewAttemptHarnessFunc before Register, without callback forwarding.
type AttemptHarnessConfig struct {
	Register agenttest.Register
	Options  []agenttest.Option
	Setup    func(*agenttest.Harness) error
}

// NewAttemptHarnessFunc constructs a fresh harness from per-attempt declarations.
// Options apply in base, configure, runner order. Conflicting evidence options
// fail before registration; runner-owned stores are never installed after setup.
// Configure receives owned metadata and must be safe for concurrent calls.
// Setup errors or panics close the constructed harness before returning an error.
func NewAttemptHarnessFunc(configure func(context.Context, AttemptInfo) (AttemptHarnessConfig, error), base ...agenttest.Option) AttemptHarnessFunc {
	base = slices.Clone(base)
	return func(ctx context.Context, info AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		if configure == nil {
			return nil, errors.New("eval: attempt harness configure callback is required")
		}
		cfg, err := configure(ctx, info)
		if err != nil {
			return nil, err
		}
		if cfg.Register == nil {
			return nil, errors.New("eval: attempt harness registration callback is required")
		}
		var setup func(*agenttest.Harness, Variant) error
		if cfg.Setup != nil {
			setup = func(h *agenttest.Harness, _ Variant) error {
				return cfg.Setup(h)
			}
		}
		factory := NewHarnessFunc(cfg.Register, setup, append(slices.Clone(base), cfg.Options...)...)
		return factory(ctx, info.Variant, opts...)
	}
}
