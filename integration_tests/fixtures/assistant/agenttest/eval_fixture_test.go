package agenttest_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	generated "example.com/assistant/gen/assistant/agents/assistant_runtime"
	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureRepeatDecideGeneratedFixture(t *testing.T) {
	t.Setenv("LOOM_LIVE_MODEL", "1")
	turns := make([]agenttest.Turn, 0, 10)
	for range 5 {
		turns = append(turns, agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})).When(agenttest.LastUserContains("status")))
	}
	for range 5 {
		turns = append(turns, agenttest.Respond(agenttest.Text("hello")).When(agenttest.LastUserContains("hello")))
	}
	client, script := agenttest.ScriptClient(t, turns...)
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("fixture", client); err != nil {
			return err
		}
		if err := generated.RegisterUsedToolsets(ctx, rt); err != nil {
			return err
		}
		return generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: fixturePlanner{}})
	}, agenttest.LiveModel("fixture"))
	r, err := h.CaptureFirstRequest(t.Context(), "assistant.assistant_runtime", agenttest.User("status?"))
	require.NoError(t, err)
	assert.Equal(t, 10, script.Remaining())
	assert.Empty(t, h.ToolCalls())
	report := agenttest.Repeat(t, 5, 4, func(at testing.TB) {
		d := agenttest.Decide(at, h.Model("fixture"), r)
		expect.Require(at, expect.Calls(d.Calls), expect.Call(projected.ProjectedStatusToolTool, func(p *projected.ProjectedStatusToolPayload) error {
			if p == nil {
				return fmt.Errorf("status payload is nil")
			}
			return nil
		}, nil))
	})
	assert.Equal(t, 5, report.Passed)
	negative := r.WithUserMessage("hello")
	report = agenttest.Repeat(t, 5, 5, func(at testing.TB) {
		d := agenttest.Decide(at, h.Model("fixture"), negative)
		expect.Require(at, expect.Calls(d.Calls), expect.Forbid(projected.ProjectedStatusToolTool))
	})
	assert.Equal(t, 5, report.Passed)
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, agenttest.SaveRequest(path, r))
	loaded := agenttest.LoadRequest(t, path)
	// Reuse the fixture with independent clients, without constructing a harness.
	turn := agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{}))
	first, _ := agenttest.ScriptClient(t, turn)
	second, _ := agenttest.ScriptClient(t, turn)
	assert.Equal(t, agenttest.Decide(t, first, r).Calls, agenttest.Decide(t, second, loaded).Calls)
}
