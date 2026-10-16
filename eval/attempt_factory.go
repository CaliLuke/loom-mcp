package eval

import (
	"context"
	"maps"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

// AttemptInfo supplies an owned snapshot of one planned attempt before harness
// construction. Key identifies its scenario, item, variant and one-based repeat.
// Item contains dataset values before scenario defaults are resolved by Execute.
// Mutating this snapshot does not change the plan, dataset or another attempt.
type AttemptInfo struct {
	Key     AttemptKey
	Item    Item
	Variant Variant
}

// AttemptHarnessFunc constructs a fresh harness using planned attempt metadata.
// Like HarnessFunc, it must forward opts to agenttest.NewHarness.
type AttemptHarnessFunc func(ctx context.Context, attempt AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error)

// harnessFactory adapts legacy factories to the same attempt ownership path.
func harnessFactory(cfg Config) AttemptHarnessFunc {
	if cfg.AttemptHarness != nil {
		return cfg.AttemptHarness
	}
	return func(ctx context.Context, info AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		return cfg.Harness(ctx, info.Variant, opts...)
	}
}

// attemptInfo owns mutable metadata independently of the planned job and repeats.
func attemptInfo(attempt plannedAttempt) AttemptInfo {
	variant := attempt.variant
	variant.Labels = maps.Clone(variant.Labels)
	variant.Policy = clonePolicy(variant.Policy)
	variant.Options = slices.Clone(variant.Options)
	return AttemptInfo{Key: attempt.key, Item: cloneItem(attempt.item), Variant: variant}
}
