package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRejectsInvalidTerminalPoliciesBeforePlanning(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*tools.ToolSpec, *PolicyOverrides)
		want string
	}{
		{"unknown completion", func(_ *tools.ToolSpec, p *PolicyOverrides) {
			p.CompletionTool = "absent"
			p.LimitTerminalPlans = nil
		}, "not registered"},
		{"restricted completion", func(s *tools.ToolSpec, p *PolicyOverrides) {
			s.TerminalRun = false
			s.Bookkeeping = false
			p.LimitTerminalPlans = nil
			p.CompletionTool = s.Name
			p.RestrictToTool = "other"
		}, "excluded"},
		{"tag excluded completion", func(s *tools.ToolSpec, p *PolicyOverrides) {
			s.TerminalRun = false
			s.Bookkeeping = false
			p.LimitTerminalPlans = nil
			p.CompletionTool = s.Name
			p.AllowedTags = []string{"other"}
		}, "excluded"},
		{"tag denied completion", func(s *tools.ToolSpec, p *PolicyOverrides) {
			s.TerminalRun = false
			s.Bookkeeping = false
			s.Tags = []string{"private"}
			p.LimitTerminalPlans = nil
			p.CompletionTool = s.Name
			p.DeniedTags = []string{"private"}
		}, "excluded"},
		{"unknown terminal", func(_ *tools.ToolSpec, p *PolicyOverrides) {
			p.LimitTerminalPlans.TimeBudget.Name = "absent"
		}, "not registered"},
		{"nonterminal", func(s *tools.ToolSpec, _ *PolicyOverrides) {
			s.TerminalRun = false
		}, "not a terminal bookkeeping"},
		{"missing payload", func(_ *tools.ToolSpec, p *PolicyOverrides) {
			p.LimitTerminalPlans.TimeBudget.Payload = nil
		}, "payload is required"},
		{"missing decoder", func(s *tools.ToolSpec, _ *PolicyOverrides) {
			s.Payload.Codec.FromJSON = nil
		}, "decoder is required"},
		{"invalid payload", func(_ *tools.ToolSpec, p *PolicyOverrides) {
			p.LimitTerminalPlans.TimeBudget.Payload = rawjson.Message("{")
		}, "payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			spec := bookkeepingSpec("reports.limit", true)
			call := LimitTerminalCall{Name: spec.Name, Payload: rawjson.Message(`{}`)}
			policy := &PolicyOverrides{LimitTerminalPlans: &LimitTerminalPlans{TimeBudget: call, ToolCallCap: call, RecoveryCap: call}}
			tc.edit(&spec, policy)
			registration := contextRegistration(rt)
			registration.Specs = []tools.ToolSpec{spec}
			called := false
			registration.Planner = &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				called = true
				return oracleFinal(), nil
			}}
			require.NoError(t, rt.RegisterAgent(t.Context(), registration))
			_, err := rt.CreateSession(t.Context(), "session")
			require.NoError(t, err)
			result, err := rt.MustClient("agent").Run(t.Context(), "session", nil, func(input *RunInput) {
				input.Policy = policy
			})
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, result)
			assert.False(t, called)
		})
	}
}
