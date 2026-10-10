package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contentRecordingEngine struct {
	engine.Engine
	activityCalls int
	queue         string
	output        *api.RecordContentOutput
}

func (e *contentRecordingEngine) RegisterRecordContentActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error)) error {
	e.queue = opts.Queue
	return e.Engine.RegisterRecordContentActivity(ctx, name, opts, func(ctx context.Context, in *api.RecordContentInput) (*api.RecordContentOutput, error) {
		e.activityCalls++
		out, err := fn(ctx, in)
		e.output = out
		return out, err
	})
}

func TestRecordContentRegistrationAndEmptyRun(t *testing.T) {
	for _, messages := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "three messages"}[messages], func(t *testing.T) {
			store := &contentPathStore{Store: storeinmem.New()}
			eng := &contentRecordingEngine{Engine: engineinmem.New()}
			rt := New(WithEngine(eng), WithRunStore(store))
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("done")}}, nil
			}})
			input := []*model.Message(nil)
			if messages {
				input = []*model.Message{contentMessage("a"), contentMessage("b"), contentMessage("c")}
			}
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", input, WithRunID("root"))
			require.NoError(t, err)
			assert.Equal(t, "content.queue", eng.queue)
			if !messages {
				assert.Zero(t, eng.activityCalls)
				return
			}
			assert.Equal(t, 1, eng.activityCalls)
			require.Len(t, eng.output.Refs, 3)
			for i, hash := range eng.output.Refs {
				assert.Equal(t, store.snapshot()[i].hash, hash)
			}
		})
	}
}

func TestRecordContentFailsWithoutPublishingPartialRefs(t *testing.T) {
	store := &contentPathStore{Store: storeinmem.New(), failAt: 2}
	rt := New(WithRunStore(store))
	out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "root", Items: [][]byte{[]byte("first"), []byte("second")}})
	require.ErrorIs(t, err, runstore.ErrContentTooLarge)
	assert.Nil(t, out)
	assert.Len(t, store.snapshot(), 1)
	_, err = rt.RecordContentActivity(t.Context(), nil)
	require.ErrorContains(t, err, "root run id")
	_, err = rt.RecordContentActivity(t.Context(), &api.RecordContentInput{})
	require.ErrorContains(t, err, "root run id")
	rt.RunStore = nil
	_, err = rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "root"})
	require.ErrorContains(t, err, "run store")
}
