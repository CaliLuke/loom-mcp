package hooks_test

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestRunStartedHookCodecAcceptsCapOnlyPolicyOverride(t *testing.T) {
	event := hooks.NewRunStartedEvent("run-1", "svc.agent", run.Context{}, api.RunInput{
		AgentID: "svc.agent",
		RunID:   "run-1",
		Policy:  &api.PolicyOverrides{MaxToolCalls: 3},
	})

	input, err := hooks.EncodeToHookInput(event, "turn-1")
	require.NoError(t, err)
	_, err = hooks.DecodeFromHookInput(input)
	require.NoError(t, err)
}

func TestRunStartedHookCodecRoundTripsPolicyDurationsAsNanoseconds(t *testing.T) {
	event := hooks.NewRunStartedEvent("run-1", "svc.agent", run.Context{}, api.RunInput{
		AgentID: "svc.agent",
		RunID:   "run-1",
		Policy: &api.PolicyOverrides{
			TimeBudget:     2 * time.Minute,
			PlanTimeout:    45 * time.Second,
			ToolTimeout:    30 * time.Second,
			PerToolTimeout: map[tools.Ident]time.Duration{"catalog.search": 5 * time.Second},
			FinalizerGrace: 10 * time.Second,
		},
	})

	input, err := hooks.EncodeToHookInput(event, "turn-1")
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(input.Payload, &payload))
	runInput := payload["Input"].(map[string]any)
	policy := runInput["Policy"].(map[string]any)
	require.InDelta(t, float64(120_000_000_000), policy["TimeBudget"], 0)
	require.InDelta(t, float64(45_000_000_000), policy["PlanTimeout"], 0)
	require.InDelta(t, float64(30_000_000_000), policy["ToolTimeout"], 0)
	require.InDelta(t, float64(10_000_000_000), policy["FinalizerGrace"], 0)
	perTool := policy["PerToolTimeout"].(map[string]any)
	require.InDelta(t, float64(5_000_000_000), perTool["catalog.search"], 0)

	decoded, err := hooks.DecodeFromHookInput(input)
	require.NoError(t, err)
	reencoded, err := hooks.EncodeToHookInput(decoded, decoded.TurnID())
	require.NoError(t, err)
	require.JSONEq(t, string(input.Payload), string(reencoded.Payload))
}
