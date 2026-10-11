package agenttest_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRunOptions(t *testing.T) {
	for _, mode := range []string{"Run", "RunSession", "CaptureFirstRequest"} {
		t.Run(mode, func(t *testing.T) {
			var inputs []runtime.RunInput
			interceptor := runtime.RuntimeInterceptorFuncs{BeforeRunFunc: func(_ context.Context, in *runtime.BeforeRunInput) (*runtime.BeforeRunDecision, error) {
				inputs = append(inputs, in.Input)
				return nil, nil
			}}
			var turns []agenttest.Turn
			if mode != "CaptureFirstRequest" {
				turns = []agenttest.Turn{agenttest.Respond(agenttest.Text("done"))}
			}
			h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model"}), agenttest.ScriptedModel("model", turns...), agenttest.WithRuntimeOptions(runtime.WithInterceptors(interceptor)), agenttest.WithDefaultRunOptions(runtime.WithLabels(map[string]string{"default": "yes"}), runtime.WithPerTurnMaxToolCalls(2)))
			switch mode {
			case "Run":
				_, err := h.Run(t.Context(), "root", nil, runtime.WithLabels(map[string]string{"caller": "yes", "default": "overridden"}), runtime.WithPerTurnMaxToolCalls(3))
				require.NoError(t, err)
			case "RunSession":
				_, err := h.RunSession(t.Context(), "root", nil)
				require.NoError(t, err)
			case "CaptureFirstRequest":
				_, err := h.CaptureFirstRequest(t.Context(), "root", nil, runtime.WithLabels(map[string]string{"caller": "yes", "default": "overridden"}), runtime.WithPerTurnMaxToolCalls(3))
				require.NoError(t, err)
			}
			require.Len(t, inputs, 1)
			if mode != "RunSession" {
				assert.Equal(t, "overridden", inputs[0].Labels["default"])
				assert.Equal(t, "yes", inputs[0].Labels["caller"])
				assert.Equal(t, 3, inputs[0].Policy.PerTurnMaxToolCalls)
			} else {
				assert.Equal(t, "yes", inputs[0].Labels["default"])
				assert.Equal(t, 2, inputs[0].Policy.PerTurnMaxToolCalls)
			}
		})
	}
}

func TestDefaultRunIDReserved(t *testing.T) {
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{}), agenttest.WithDefaultRunOptions(runtime.WithRunID("")))
	_, err := h.Run(t.Context(), "root", nil)
	require.ErrorContains(t, err, "RunID is reserved")
}
