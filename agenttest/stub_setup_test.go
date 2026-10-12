package agenttest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubSetupErrorsBeforeRun(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      func(*agenttest.Harness)
	}{
		{"payload decoder", "payload decoder is required", func(h *agenttest.Harness) {
			tool := echoTool()
			tool.Payload.FromJSON = nil
			agenttest.Stub(h, tool)
		}},
		{"nil failure", "Fail requires an error", func(h *agenttest.Harness) { agenttest.Stub(h, echoTool()).Fail(nil) }},
		{"nil handler", "Handle requires a callback", func(h *agenttest.Harness) { agenttest.Stub(h, echoTool()).Handle(nil) }},
		{"zero times", "Times must be positive", func(h *agenttest.Harness) { agenttest.Stub(h, echoTool()).Return("unused").Times(0) }},
		{"negative delay", "After must not be negative", func(h *agenttest.Harness) { agenttest.Stub(h, echoTool()).Return("unused").After(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tb := &recordingTB{TB: t, ctx: t.Context()}
			h := agenttest.New(tb, registerHarnessAgent("root", harnessPlanner{specs: []tools.ToolSpec{echoSpec()}}))
			tc.setup(h)
			require.Len(t, tb.errors, 1)
			assert.Contains(t, tb.errors[0], tc.want)
			result, err := h.Run(t.Context(), "root", nil)
			assert.Nil(t, result)
			require.ErrorContains(t, err, tc.want)
			assert.ErrorContains(t, h.Close(), tc.want)
		})
	}
}

func TestRawStubRequiresResultDecoder(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), func(_ context.Context, rt *runtime.Runtime) error {
		spec := echoSpec()
		spec.Result.Codec.FromJSON = nil
		return rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			return nil, errors.New("must not execute")
		}})
	})
	require.NoError(t, err)
	agenttest.RawStub(h, "echo.echo")
	result, err := h.Run(t.Context(), "root", nil)
	assert.Nil(t, result)
	require.ErrorContains(t, err, "registered result decoder")
	assert.ErrorContains(t, h.Close(), "registered result decoder")
}
