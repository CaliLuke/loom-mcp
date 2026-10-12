package selvedge

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplayHistoryWrapper(t *testing.T) {
	var effects atomic.Int32
	register := func(e *Engine, changed bool) {
		require.NoError(t, e.RegisterExecuteToolActivity(t.Context(), "effect", engine.ActivityOptions{}, func(context.Context, *api.ToolInput) (*api.ToolOutput, error) {
			effects.Add(1)
			return &api.ToolOutput{}, nil
		}))
		require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "replay", Handler: func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
			if in.WorkflowProtocolVersion != api.WorkflowProtocolVersion {
				return nil, &api.WorkflowProtocolMismatchError{Want: api.WorkflowProtocolVersion, Got: in.WorkflowProtocolVersion}
			}
			if changed {
				return &api.RunOutput{RunID: "changed"}, nil
			}
			return toolWorkflow("effect")(w, in)
		}}))
	}
	live := newTestEngine(t)
	register(live, false)
	require.NoError(t, live.SealRegistration(t.Context()))
	h, err := live.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "replay", Workflow: "replay", Input: &api.RunInput{WorkflowProtocolVersion: api.WorkflowProtocolVersion}})
	require.NoError(t, err)
	_, err = h.Wait(t.Context())
	require.NoError(t, err)
	exported, err := sv.ExportHistory(t.Context(), live.client, "replay")
	require.NoError(t, err)
	t.Run("export", func(t *testing.T) {
		got, err := live.ExportHistory(t.Context(), "replay")
		require.NoError(t, err)
		assert.Equal(t, exported, got)
	})
	raw, err := json.Marshal(exported)
	require.NoError(t, err)
	recorded, err := sv.ParseHistory(raw)
	require.NoError(t, err)
	assert.EqualValues(t, 1, effects.Load())
	effects.Store(0)
	offline := newTestEngine(t)
	register(offline, false)
	require.NoError(t, offline.ReplayHistory(t.Context(), recorded))
	assert.Zero(t, effects.Load())
	for _, protocol := range []int{0, api.WorkflowProtocolVersion + 1} {
		altered, err := sv.ParseHistory(raw)
		require.NoError(t, err)
		altered.Instance.Input, err = json.Marshal(&api.RunInput{WorkflowProtocolVersion: protocol})
		require.NoError(t, err)
		err = offline.ReplayHistory(t.Context(), altered)
		require.ErrorIs(t, err, api.ErrWorkflowProtocolMismatch)
		assert.Contains(t, err.Error(), "workflow protocol mismatch")
	}
	changed := newTestEngine(t)
	register(changed, true)
	var divergence *sv.ReplayError
	require.ErrorAs(t, changed.ReplayHistory(t.Context(), recorded), &divergence)
	require.Error(t, divergence.Divergence)
	assert.Zero(t, effects.Load())
}
