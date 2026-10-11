package agenttest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveHelpers(t *testing.T) {
	for _, tc := range []struct {
		value            string
		enabled, invalid bool
	}{
		{"", false, false}, {"0", false, false}, {"1", true, false}, {"yes", false, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("LOOM_LIVE_MODEL", tc.value)
			enabled, err := agenttest.LiveModelEnabled()
			assert.Equal(t, tc.enabled, enabled)
			assert.Equal(t, tc.invalid, err != nil)
			tb := &evalTB{TB: t}
			runTB(func() {
				agenttest.SkipUnlessLive(tb, "named-model")
			})
			assert.Equal(t, !tc.enabled && !tc.invalid, tb.skipped)
			assert.Equal(t, tc.invalid, tb.failed)
			if tb.skipped {
				assert.Contains(t, tb.diagnostics[0], "named-model")
			}
		})
	}
}

func TestLiveModelConstruction(t *testing.T) {
	for _, value := range []string{"", "0", "yes"} {
		t.Run("disabled-"+value, func(t *testing.T) {
			t.Setenv("LOOM_LIVE_MODEL", value)
			called := false
			register := func(context.Context, *runtime.Runtime) error {
				called = true
				return fmt.Errorf("registration must not run")
			}
			_, err := agenttest.NewHarness(t.Context(), register, agenttest.LiveModel("named-model"))
			if value != "yes" {
				require.ErrorIs(t, err, agenttest.ErrLiveModelDisabled)
			} else {
				require.Error(t, err)
			}
			assert.False(t, called)
			tb := &evalTB{TB: t}
			runTB(func() {
				agenttest.New(tb, register, agenttest.LiveModel("named-model"))
			})
			assert.Equal(t, value != "yes", tb.skipped)
			assert.Equal(t, value == "yes", tb.failed)
			assert.False(t, called)
			if value != "yes" {
				assert.Contains(t, tb.diagnostics[0], "named-model")
			}
		})
	}
	t.Setenv("LOOM_LIVE_MODEL", "1")
	script, err := agenttest.NewScript()
	require.NoError(t, err)
	register := func(_ context.Context, rt *runtime.Runtime) error {
		return rt.RegisterModel("live", script.Client())
	}
	h := agenttest.New(t, register, agenttest.LiveModel("live"))
	assert.Equal(t, script.Client(), h.Model("live"))
	_, err = agenttest.NewHarness(t.Context(), register, agenttest.LiveModel("missing"))
	require.ErrorContains(t, err, `application registered no client for "missing"`)
	for _, options := range [][]agenttest.Option{
		{agenttest.LiveModel("live"), agenttest.ScriptedModel("live")},
		{agenttest.ScriptedModel("live"), agenttest.LiveModel("live")},
	} {
		_, err = agenttest.NewHarness(t.Context(), register, options...)
		require.Error(t, err)
	}
}
