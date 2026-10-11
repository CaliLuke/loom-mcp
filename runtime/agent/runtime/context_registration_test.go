package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextRegistrationConflicts(t *testing.T) {
	cases := []struct {
		name   string
		option RuntimeOption
		policy RunPolicy
		want   string
	}{
		{"compaction", WithContextCompaction("agent", ContextCompaction{}), RunPolicy{ContextCompaction: &ContextCompaction{}}, "agent \"agent\": RunPolicy.ContextCompaction and WithContextCompaction cannot both be configured"},
		{"recovery", WithContextRecovery("agent", ContextRecovery{}), RunPolicy{ContextRecovery: &ContextRecovery{DisableRecall: true}}, "agent \"agent\": RunPolicy.ContextRecovery and WithContextRecovery cannot both be configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithEngine(inmem.New()), tc.option)
			reg := contextRegistration(rt)
			reg.Policy = tc.policy
			err := rt.RegisterAgent(t.Context(), reg)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestContextRegistrationStampsSettingsAndKeepUnion(t *testing.T) {
	rt := New(WithEngine(inmem.New()))
	reg := contextRegistration(rt)
	cfg := &ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 7, Bytes: 10}, KeepRecentTokens: 1200, KeepResultTools: []tools.Ident{"tools.option"}, Summary: SummarySettings{Instructions: "keep IDs"}}
	reg.Policy = RunPolicy{ContextCompaction: cfg, ContextRecovery: &ContextRecovery{DisableRecall: true}}
	reg.Specs = []tools.ToolSpec{{Name: "tools.keep", KeepResultInContext: true}, {Name: "tools.clear"}}
	require.NoError(t, rt.RegisterAgent(t.Context(), reg))
	cfg.KeepRecentTokens = 999
	input := RunInput{AgentID: "agent"}
	rt.stampContextCompaction(&input)
	require.NotNil(t, input.ContextCompaction)
	assert.Equal(t, 1200, input.ContextSettings.KeepRecentTokens)
	assert.Equal(t, contextmeasure.Rate{Tokens: 7, Bytes: 10}, input.ContextSettings.TriggerAt)
	assert.True(t, input.ContextSettings.Threshold)
	assert.True(t, input.ContextSettings.DisableRecall)
	assert.Equal(t, "keep IDs", input.ContextSettings.Summary.Settings.Instructions)
	assert.ElementsMatch(t, []tools.Ident{"tools.keep", "tools.option"}, input.ContextCompaction.KeepResultTools)
}

func TestContextRegistrationInvalidPolicy(t *testing.T) {
	cases := []struct {
		name   string
		config ContextCompaction
	}{
		{"negative trigger", ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: -1, Bytes: 100}}},
		{"missing denominator", ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1}}},
		{"missing numerator", ContextCompaction{TriggerAt: contextmeasure.Rate{Bytes: 100}}},
		{"trigger above one", ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 101, Bytes: 100}}},
		{"negative recent", ContextCompaction{KeepRecentTokens: -1}},
		{"negative protection", ContextCompaction{ProtectRecentToolTokens: -1}},
		{"negative saving", ContextCompaction{MinSavingTokens: -1}},
		{"negative discovery", ContextCompaction{RecallDiscoveryCalls: -1}},
		{"negative reads", ContextCompaction{RecallReadCalls: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			reg := contextRegistration(rt)
			reg.Policy.ContextCompaction = &tc.config
			var err error
			require.NotPanics(t, func() {
				err = rt.RegisterAgent(t.Context(), reg)
			})
			assert.ErrorIs(t, err, ErrInvalidConfig)
		})
	}
}

func TestContextRegistrationRepeat(t *testing.T) {
	cases := []struct {
		name   string
		engine engine.Engine
		want   string
	}{
		{"inmem", inmem.New(), `workflow "workflow" already registered`},
		{"permissive", &stubEngine{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithEngine(tc.engine))
			reg := contextRegistration(rt)
			reg.Policy = RunPolicy{ContextCompaction: &ContextCompaction{}, ContextRecovery: &ContextRecovery{DisableRecall: true}}
			require.NoError(t, rt.RegisterAgent(t.Context(), reg))
			err := rt.RegisterAgent(t.Context(), reg)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.want)
			}
		})
	}
}

func contextRegistration(rt *Runtime) AgentRegistration {
	return AgentRegistration{
		ID: "agent", Planner: &stubPlanner{}, Workflow: engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool",
	}
}
