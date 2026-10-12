package agenttest_test

import (
	"fmt"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOpenSessionIncremental(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("first reply")), agenttest.Respond(agenttest.Text("second reply"))))
	session, err := h.OpenSession(t.Context())
	require.NoError(t, err)
	first, err := session.Run(t.Context(), "root", agenttest.User("first"))
	require.NoError(t, err)
	snapshot := session.Result()
	require.Len(t, snapshot.Runs, 1)
	second, err := session.Run(t.Context(), "root", agenttest.User("second"))
	require.NoError(t, err)
	assert.Equal(t, first.SessionID, second.SessionID)
	assert.Len(t, session.Result().Runs, 2)
	assert.Len(t, snapshot.Runs, 1)
}

func TestSessionContinuationThresholdOnly(t *testing.T) {
	for _, threshold := range []bool{false, true} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.WithRuntimeOptions(runtime.WithContextCompaction("root", runtime.ContextCompaction{})), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("first reply")), agenttest.Respond(agenttest.Text("second reply"))))
			session, err := h.OpenSession(t.Context())
			require.NoError(t, err)
			opts := runtime.WithContextOverrides(runtime.ContextOverrides{Threshold: new(threshold), ContextWindow: new(24000)})
			first, err := session.Run(t.Context(), "root", agenttest.User("first"), opts)
			require.NoError(t, err)
			require.NoError(t, first.RunErr)
			second, err := session.Run(t.Context(), "root", agenttest.Input{agenttest.User("first")[0], first.Output.Final, agenttest.User("second")[0]}, opts)
			require.NoError(t, err)
			require.NoError(t, second.RunErr)
			continued := false
			for _, r := range second.Record.Runs {
				for _, e := range r.Events {
					if e.Type == hooks.ContinuationStarted {
						assert.Contains(t, string(e.Payload), `"outcome":"continued"`)
						continued = true
					}
				}
			}
			assert.Equal(t, threshold, continued)
		})
	}
}
