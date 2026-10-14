package suite

import (
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// LookWork is the exact work allowed by a verified policy and its stored prefix.
// Keys is nonnil only for a same-look infrastructure rerun.
type LookWork struct {
	Variants []string
	Keys     []eval.AttemptKey
	Rerun    bool
}

// PlanLook validates scheduling before any experiment or harness is created.
func PlanLook(policy *gate.Policy, look int, previous []gate.Decision) (LookWork, error) {
	if err := gate.ValidateNextLook(policy, look, previous); err != nil {
		return LookWork{}, err
	}
	work := LookWork{Variants: []string{"baseline"}}
	active := map[string]bool{}
	for name := range policy.Unsupported {
		active[name] = true
	}
	if len(previous) > 0 {
		last := previous[len(previous)-1]
		if last.Status == gate.Incomplete {
			work.Rerun = true
			work.Keys = make([]eval.AttemptKey, 0, len(last.Rerun))
			for _, key := range last.Rerun {
				work.Keys = append(work.Keys, eval.AttemptKey{Variant: key.Variant, Scenario: key.Scenario, Item: key.Item, Repeat: key.Repeat})
			}
		} else {
			for _, v := range last.Variants {
				active[v.Variant] = v.Status == gate.Continue
			}
		}
	}
	for _, name := range []string{"receipt_only", "hybrid"} {
		if active[name] {
			work.Variants = append(work.Variants, name)
		}
	}
	if look == 1 {
		work.Variants = append(work.Variants, "clear_only")
	}
	return work, nil
}
