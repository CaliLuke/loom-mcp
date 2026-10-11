package eval

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"runtime/debug"
	"slices"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

// Variant configures one isolated harness and its run defaults.
type Variant struct {
	ID      string
	Labels  map[string]string
	Policy  *api.PolicyOverrides
	Options []agenttest.Option
}

// HarnessFunc constructs a fresh harness per attempt and forwards opts to NewHarness.
type HarnessFunc func(ctx context.Context, v Variant, opts ...agenttest.Option) (*agenttest.Harness, error)

// DefaultVariant identifies the implicit variant when no variants are declared.
const DefaultVariant = "default"

// NewHarnessFunc constructs isolated harnesses with base options before variant options.
// A failed setup closes its harness before returning the joined error.
func NewHarnessFunc(register agenttest.Register, setup func(*agenttest.Harness, Variant) error, base ...agenttest.Option) HarnessFunc {
	base = slices.Clone(base)
	return func(ctx context.Context, v Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		h, err := agenttest.NewHarness(ctx, register, append(slices.Clone(base), opts...)...)
		if err != nil {
			return nil, err
		}
		if setup != nil {
			if err := callSetup(setup, h, v); err != nil {
				return nil, errors.Join(err, h.Close())
			}
		}
		return h, nil
	}
}

// callSetup converts setup panics into errors so the builder can close its harness.
func callSetup(setup func(*agenttest.Harness, Variant) error, h *agenttest.Harness, v Variant) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("eval: harness setup panic: %v\n%s", value, debug.Stack())
		}
	}()
	return setup(h, v)
}

func variantRunOptions(v Variant) []agenttest.RunOption {
	return []agenttest.RunOption{runtime.WithLabels(v.Labels), func(in *runtime.RunInput) {
		if v.Policy != nil {
			in.Policy = clonePolicy(v.Policy)
		}
	}}
}

// clonePolicy owns every mutable policy member for each invocation.
func clonePolicy(p *api.PolicyOverrides) *api.PolicyOverrides {
	if p == nil {
		return nil
	}
	owned := *p
	owned.AllowedTags = slices.Clone(p.AllowedTags)
	owned.DeniedTags = slices.Clone(p.DeniedTags)
	owned.PerToolTimeout = maps.Clone(p.PerToolTimeout)
	if p.LimitTerminalPlans != nil {
		plans := *p.LimitTerminalPlans
		plans.TimeBudget.Payload = slices.Clone(plans.TimeBudget.Payload)
		plans.ToolCallCap.Payload = slices.Clone(plans.ToolCallCap.Payload)
		plans.RecoveryCap.Payload = slices.Clone(plans.RecoveryCap.Payload)
		owned.LimitTerminalPlans = &plans
	}
	return &owned
}

func validateVariants(variants []Variant) []error {
	var problems []error
	seen := make(map[string]bool, len(variants))
	for _, v := range variants {
		if !idPattern.MatchString(v.ID) {
			problems = append(problems, fmt.Errorf("eval: invalid variant ID %q", v.ID))
		}
		if seen[v.ID] {
			problems = append(problems, fmt.Errorf("eval: duplicate variant %q", v.ID))
		}
		seen[v.ID] = true
		for k, value := range v.Labels {
			if k == "" || !utf8.ValidString(k) || !utf8.ValidString(value) {
				problems = append(problems, fmt.Errorf("eval: variant %q label must have a non-empty key and valid UTF-8", v.ID))
			}
		}
		if p := v.Policy; p != nil {
			if p.PerTurnMaxToolCalls < 0 || p.MaxToolCalls < 0 || p.MaxRecoveryTurns < 0 || p.TimeBudget < 0 || p.PlanTimeout < 0 || p.ToolTimeout < 0 || p.FinalizerGrace < 0 {
				problems = append(problems, fmt.Errorf("eval: variant %q Policy counts and durations must be non-negative", v.ID))
			}
			for _, d := range p.PerToolTimeout {
				if d < 0 {
					problems = append(problems, fmt.Errorf("eval: variant %q Policy tool duration must be non-negative", v.ID))
				}
			}
		}
	}
	return problems
}
