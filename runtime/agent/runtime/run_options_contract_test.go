package runtime

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTimingOverridesMergeWithoutResettingExistingBudget(t *testing.T) {
	var input RunInput
	perTool := map[tools.Ident]time.Duration{"read": time.Second}
	WithTiming(Timing{Budget: time.Minute, Plan: 10 * time.Second, Tools: 2 * time.Second, PerToolTimeout: perTool})(&input)
	WithTiming(Timing{Plan: 5 * time.Second, PerToolTimeout: map[tools.Ident]time.Duration{"write": 3 * time.Second}})(&input)
	WithTiming(Timing{})(&input)
	require.NotNil(t, input.Policy)
	assert.Equal(t, time.Minute, input.Policy.TimeBudget)
	assert.Equal(t, 5*time.Second, input.Policy.PlanTimeout)
	assert.Equal(t, 2*time.Second, input.Policy.ToolTimeout)
	assert.Equal(t, map[tools.Ident]time.Duration{"read": time.Second, "write": 3 * time.Second}, input.Policy.PerToolTimeout)
	perTool["read"] = time.Hour
	assert.Equal(t, time.Second, input.Policy.PerToolTimeout["read"])
}

func TestRunMetadataOptionsMergeWithoutSharingCallerMaps(t *testing.T) {
	var input RunInput
	labels := map[string]string{"tenant": "one"}
	meta := map[string]any{"key": "one"}
	memo := map[string]any{"key": "one"}
	search := map[string]any{"key": "one"}
	WithLabels(labels)(&input)
	WithLabels(map[string]string{"run": "two"})(&input)
	WithMetadata(meta)(&input)
	WithMetadata(nil)(&input)
	WithMetadata(map[string]any{"run": "two"})(&input)
	WithMemo(memo)(&input)
	WithMemo(map[string]any{"run": "two"})(&input)
	WithSearchAttributes(search)(&input)
	WithSearchAttributes(map[string]any{"run": "two"})(&input)
	WithTaskQueue("worker")(&input)
	labels["tenant"] = "mutated"
	meta["key"] = "mutated"
	memo["key"] = "mutated"
	search["key"] = "mutated"
	assert.Equal(t, map[string]string{"tenant": "one", "run": "two"}, input.Labels)
	assert.Equal(t, map[string]any{"key": "one", "run": "two"}, input.Metadata)
	require.NotNil(t, input.WorkflowOptions)
	assert.Equal(t, "worker", input.WorkflowOptions.TaskQueue)
	assert.Equal(t, map[string]any{"key": "one", "run": "two"}, input.WorkflowOptions.Memo)
	assert.Equal(t, map[string]any{"key": "one", "run": "two"}, input.WorkflowOptions.SearchAttributes)
}

func TestRunPolicyOptionsComposeAndOwnFilters(t *testing.T) {
	for _, initial := range []*PolicyOverrides{nil, {MaxToolCalls: 7}} {
		input := RunInput{Policy: initial}
		allow, deny := []string{"read"}, []string{"dangerous"}
		WithRunFinalizerGrace(3 * time.Second)(&input)
		WithRunInterruptsAllowed(true)(&input)
		WithRestrictToTool("docs.read")(&input)
		WithAllowedTags(allow)(&input)
		WithDeniedTags(deny)(&input)
		allow[0], deny[0] = "changed", "changed"
		assert.Equal(t, 3*time.Second, input.Policy.FinalizerGrace)
		assert.True(t, input.Policy.InterruptsAllowed)
		assert.Equal(t, tools.Ident("docs.read"), input.Policy.RestrictToTool)
		assert.Equal(t, []string{"read"}, input.Policy.AllowedTags)
		assert.Equal(t, []string{"dangerous"}, input.Policy.DeniedTags)
		WithRunInterruptsAllowed(false)(&input)
		WithAllowedTags(nil)(&input)
		WithDeniedTags(nil)(&input)
		assert.False(t, input.Policy.InterruptsAllowed)
		assert.Empty(t, input.Policy.AllowedTags)
		assert.Empty(t, input.Policy.DeniedTags)
		assert.Equal(t, 3*time.Second, input.Policy.FinalizerGrace)
	}
}
