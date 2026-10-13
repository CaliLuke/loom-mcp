package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextDefaultStampAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		options                  []RuntimeOption
		def, threshold, resident bool
	}{
		{name: "planner", def: true, resident: true},
		{name: "explicit resident", options: []RuntimeOption{WithContextCompaction("content.agent", ContextCompaction{KeepNonTextResident: true})}, threshold: true, resident: true},
		{name: "disabled", options: []RuntimeOption{WithoutContextDefault("content.agent")}},
		{name: "explicit wins disabled", options: []RuntimeOption{WithoutContextDefault("content.agent"), WithContextCompaction("content.agent", ContextCompaction{})}, threshold: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(tc.options...)
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return oracleFinal(), nil
			}})
			input := &RunInput{AgentID: "content.agent", RunID: "run", RootRunID: "run"}
			rt.stampContextCompaction(input)
			assert.Equal(t, tc.def, input.ContextSettings.Default)
			assert.Equal(t, tc.threshold, input.ContextSettings.Threshold)
			assert.Equal(t, tc.resident, input.ContextSettings.KeepNonTextResident)
			if tc.def {
				assert.Contains(t, input.ContextDefaultAgents, input.AgentID)
			}
			require.NoError(t, rt.prepareContextStart(t.Context(), input))
			require.NotEmpty(t, input.ContextFingerprint)
			raw, err := rt.RunStore.Get(t.Context(), "run", runstore.Hash(input.ContextFingerprint))
			require.NoError(t, err)
			var settings compaction.Settings
			err = compaction.DecodeCanonical(raw, &settings)
			require.NoError(t, err)
			assert.Equal(t, tc.def, settings.Default)
		})
	}
}

func TestContextDefaultWorkflowOnlyDoesNotOptIn(t *testing.T) {
	rt := New()
	require.NoError(t, rt.Engine.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
		return &api.RunOutput{}, nil
	}}))
	input := &RunInput{AgentID: "workflow"}
	rt.stampContextCompaction(input)
	assert.False(t, input.ContextSettings.Default)
}

func TestContextDefaultChildSnapshot(t *testing.T) {
	rt := New()
	registerContentAgent(t, rt, &stubPlanner{})
	parent := &RunInput{AgentID: "content.agent"}
	rt.stampContextCompaction(parent)
	require.Contains(t, parent.ContextDefaultAgents, parent.AgentID)
	native := &testWorkflowContext{ctx: t.Context()}
	wrapper := compactionWorkflowContext{WorkflowContext: native, defaults: parent.ContextDefaultAgents}
	child := &RunInput{AgentID: parent.AgentID}
	inheritCompaction(wrapper.Context(), child)
	assert.True(t, child.ContextSettings.Default)
	snapshot := parent.ContextDefaultAgents
	snapshot[parent.AgentID] = ContextDefault{Disabled: true}
	fresh := &RunInput{AgentID: parent.AgentID}
	rt.stampContextCompaction(fresh)
	assert.True(t, fresh.ContextSettings.Default, "stamped map must not mutate registration")
}

func TestContextDefaultRecallConflictDisablesOnlyDefault(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		rt := New()
		reg := contextRegistration(rt)
		reg.Specs = []tools.ToolSpec{{Name: "tools.recall_context_legacy"}}
		if explicit {
			reg.Policy.ContextCompaction = &ContextCompaction{}
		}
		err := rt.RegisterAgent(t.Context(), reg)
		if explicit {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		input := &RunInput{AgentID: reg.ID}
		rt.stampContextCompaction(input)
		assert.False(t, input.ContextSettings.Default)
	}
}

type defaultConflictLogger struct {
	telemetry.NoopLogger
	warnings int
}

func (l *defaultConflictLogger) Warn(context.Context, string, ...any) {
	l.warnings++
}
func TestContextDefaultConflictLogsOnce(t *testing.T) {
	logger := &defaultConflictLogger{}
	rt := New(WithLogger(logger))
	reg := contextRegistration(rt)
	reg.Specs = []tools.ToolSpec{{Name: "tools.recall_context_legacy"}}
	require.NoError(t, rt.RegisterAgent(t.Context(), reg))
	rt.stampContextCompaction(&RunInput{AgentID: reg.ID})
	rt.stampContextCompaction(&RunInput{AgentID: reg.ID})
	assert.Equal(t, 1, logger.warnings)
}

func TestContextDefaultSummaryModelValidatedBeforeActivation(t *testing.T) {
	rt := New()
	reg := contextRegistration(rt)
	reg.Policy.ContextDefault = &ContextDefault{ContextModel: &ContextModel{ID: "missing"}}
	require.NoError(t, rt.RegisterAgent(t.Context(), reg))
	require.Error(t, rt.Seal(t.Context()))
}
