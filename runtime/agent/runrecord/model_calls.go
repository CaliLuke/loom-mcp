package runrecord

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
)

// recordModelCallLimit retains refusal evidence without treating its reserved
// identity as a started model call or adding it to the adopted call set.
func (r *runReader) recordModelCallLimit(limit *policy.ModelCallLimit) error {
	if limit == nil {
		return nil
	}
	if err := limit.Validate(); err != nil {
		return err
	}
	if limit.Call.RunID != r.run.RunID {
		return fmt.Errorf("model call limit run identity mismatch")
	}
	if _, exists := r.byKey[limit.Call]; exists {
		return fmt.Errorf("refused model reservation has a started call")
	}
	if r.run.ModelCallLimit != nil && *r.run.ModelCallLimit != *limit {
		return fmt.Errorf("conflicting model call limit evidence")
	}
	copy := *limit
	r.run.ModelCallLimit = &copy
	return nil
}
