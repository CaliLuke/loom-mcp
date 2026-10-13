package agentfeatures_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type forbiddenReplayStore struct{ calls *atomic.Int64 }
type forbiddenReplayLog struct{ calls *atomic.Int64 }
type changedReplayModel struct {
	calls   *atomic.Int64
	support model.ToolResultPartSupport
}

func (s forbiddenReplayStore) Get(context.Context, string, runstore.Hash) ([]byte, error) {
	s.calls.Add(1)
	return nil, runstore.ErrNotFound
}
func (s forbiddenReplayStore) Put(context.Context, string, []byte) (runstore.Hash, error) {
	s.calls.Add(1)
	return "", runstore.ErrNotFound
}
func (s forbiddenReplayStore) PutCall(context.Context, string, runstore.CallRecord) error {
	s.calls.Add(1)
	return runstore.ErrNotFound
}
func (s forbiddenReplayStore) ListCalls(context.Context, string, string) ([]runstore.CallRecord, error) {
	s.calls.Add(1)
	return nil, runstore.ErrNotFound
}
func (s forbiddenReplayStore) NextSeq(context.Context, string, string) (int, error) {
	s.calls.Add(1)
	return 0, runstore.ErrNotFound
}
func (s forbiddenReplayStore) DeleteScope(context.Context, string) error {
	s.calls.Add(1)
	return runstore.ErrNotFound
}
func (s forbiddenReplayLog) List(context.Context, string, string, int) (runlog.Page, error) {
	s.calls.Add(1)
	return runlog.Page{}, runstore.ErrNotFound
}
func (s forbiddenReplayLog) Append(context.Context, *runlog.Event) (runlog.AppendResult, error) {
	s.calls.Add(1)
	return runlog.AppendResult{}, runstore.ErrNotFound
}
func (c changedReplayModel) Complete(context.Context, *model.Request) (*model.Response, error) {
	c.calls.Add(1)
	return nil, model.ErrStreamingUnsupported
}
func (c changedReplayModel) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	c.calls.Add(1)
	return nil, model.ErrStreamingUnsupported
}
func (c changedReplayModel) ProviderConfiguration(context.Context, *model.Request) string {
	c.calls.Add(1)
	return "changed-unqualified"
}
func (c changedReplayModel) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	c.calls.Add(1)
	return c.support
}
func (c changedReplayModel) ContextLimits(context.Context, *model.Request) (model.ContextLimits, string, bool) {
	c.calls.Add(1)
	return model.ContextLimits{ContextWindow: 999999}, "different-model", true
}

func forbidDefaultReplayEffects(t *testing.T, fx *featureRuntime) *atomic.Int64 {
	t.Helper()
	calls := &atomic.Int64{}
	require.NoError(t, fx.rt.RegisterModel("history", changedReplayModel{calls: calls}))
	fx.rt.RunStore = forbiddenReplayStore{calls: calls}
	fx.rt.RunEventStore = forbiddenReplayLog{calls: calls}
	return calls
}

func assertDefaultReplayEvidence(t *testing.T, observed *replayCommandObservations, resident bool) {
	t.Helper()
	require.Len(t, observed.planners, 2)
	require.Len(t, observed.outputs, 2)
	for _, input := range observed.planners {
		assert.True(t, input.ContextSettings.Default)
		assert.True(t, input.ContextSettings.KeepNonTextResident)
		assert.False(t, input.ContextSettings.Threshold)
	}
	for _, output := range observed.outputs {
		require.NotEmpty(t, output.Facts)
		assert.Equal(t, "fixture-qualified", output.Facts[0].ProviderConfig)
		assert.Equal(t, compaction.Evict, output.Facts[0].Qualified)
		assert.Equal(t, []model.ImageFormat{"jpeg"}, output.Facts[0].ToolResultParts.ImageFormats)
	}
	if resident {
		require.Len(t, observed.compatibilities, 1)
		assert.True(t, observed.compatibilities[0].Evidence.Checked)
		assert.False(t, observed.compatibilities[0].Evidence.Compatible)
		assert.Equal(t, "too_large", observed.compatibilities[0].Evidence.Reason)
		assert.Equal(t, "history", observed.compatibilities[0].Evidence.Model)
	} else {
		assert.Empty(t, observed.compatibilities)
	}
}
