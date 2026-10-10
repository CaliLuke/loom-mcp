package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type callInfoObservation struct {
	info    model.CallInfo
	present bool
}

type callInfoClient struct{ calls chan callInfoObservation }

type callInfoPlanner struct {
	call func(context.Context, planner.PlannerContext) error
}

type failingSequenceStore struct {
	runstore.Store
	err error
}

func TestPlannerModelCallInfo(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "resume"}[resume], func(t *testing.T) {
			rt, client, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
				c, ok := agent.ModelClient("registered-model")
				require.True(t, ok)
				_, err := c.Complete(ctx, &model.Request{Model: "provider-model"})
				require.NoError(t, err)
				stream, err := c.Stream(ctx, &model.Request{Model: "provider-model"})
				require.NoError(t, err)
				_, err = stream.Recv()
				require.ErrorIs(t, err, io.EOF)
				return stream.Finalize(nil)
			})
			ctx := engine.WithActivityAttempt(context.Background(), 1)
			var err error
			if resume {
				_, err = rt.PlanResumeActivity(ctx, input)
			} else {
				_, err = rt.PlanStartActivity(ctx, input)
			}
			require.NoError(t, err)
			for seq := 1; seq <= 2; seq++ {
				got := <-client.calls
				assert.True(t, got.present)
				assert.Equal(t, model.CallInfo{AgentID: "svc.agent", RunID: "child-run", RootRunID: "root-run", SessionID: "session", TurnID: "turn", ModelID: "registered-model", Seq: seq, Attempt: 1}, got.info)
			}
			next, err := rt.RunStore.NextSeq(ctx, "root-run", "child-run")
			require.NoError(t, err)
			assert.Equal(t, 3, next)
		})
	}
}

func TestPlannerModelCallInfoParallel(t *testing.T) {
	rt, client, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		c, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for range 20 {
			wg.Go(func() {
				_, err := c.Complete(ctx, &model.Request{Model: "provider-model"})
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		var all []error
		for err := range errs {
			all = append(all, err)
		}
		return errors.Join(all...)
	})
	_, err := rt.PlanStartActivity(engine.WithActivityAttempt(context.Background(), 1), input)
	require.NoError(t, err)
	require.Len(t, client.calls, 20)
	seen := make(map[int]bool)
	for range 20 {
		got := <-client.calls
		assert.True(t, got.present)
		assert.GreaterOrEqual(t, got.info.Seq, 1)
		assert.Equal(t, 1, got.info.Attempt)
		assert.False(t, seen[got.info.Seq])
		seen[got.info.Seq] = true
	}
}

func TestPlannerModelCallInfoRetry(t *testing.T) {
	lateFailure := errors.New("failure after model call")
	rt, client, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		c, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		_, err := c.Complete(ctx, &model.Request{Model: "provider-model"})
		if err != nil {
			return err
		}
		if engine.ActivityAttemptFromContext(ctx) == 1 {
			return lateFailure
		}
		return nil
	})
	// Retrying activity harness: the same input and store survive a late failure.
	_, err := rt.PlanStartActivity(engine.WithActivityAttempt(context.Background(), 1), input)
	require.ErrorIs(t, err, lateFailure)
	_, err = rt.PlanStartActivity(engine.WithActivityAttempt(context.Background(), 2), input)
	require.NoError(t, err)
	first, second := <-client.calls, <-client.calls
	assert.Equal(t, 1, first.info.Attempt)
	assert.Equal(t, 2, second.info.Attempt)
	assert.Equal(t, 1, first.info.Seq)
	assert.Equal(t, 2, second.info.Seq)
}

func TestPlannerModelCallInfoSequenceFailure(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "stream"}[stream], func(t *testing.T) {
			rt, client, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
				c, ok := agent.ModelClient("registered-model")
				require.True(t, ok)
				if stream {
					_, err := c.Stream(ctx, &model.Request{Model: "provider-model"})
					return err
				}
				_, err := c.Complete(ctx, &model.Request{Model: "provider-model"})
				return err
			})
			allocationFailure := errors.New("sequence unavailable")
			rt.RunStore = failingSequenceStore{Store: rt.RunStore, err: allocationFailure}
			_, err := rt.PlanStartActivity(engine.WithActivityAttempt(context.Background(), 1), input)
			require.ErrorIs(t, err, allocationFailure)
			assert.Empty(t, client.calls)
		})
	}
}

func TestRuntimeModelClientDoesNotAddCallInfo(t *testing.T) {
	rt := New()
	observed := &callInfoClient{calls: make(chan callInfoObservation, 2)}
	require.NoError(t, rt.RegisterModel("registered-model", observed))
	client, ok := rt.ModelClient("registered-model")
	require.True(t, ok)
	_, err := client.Complete(context.Background(), &model.Request{})
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	assert.False(t, (<-observed.calls).present)
	assert.False(t, (<-observed.calls).present)
}

func (c *callInfoClient) Complete(ctx context.Context, _ *model.Request) (*model.Response, error) {
	info, ok := model.CallInfoFrom(ctx)
	c.calls <- callInfoObservation{info: info, present: ok}
	return &model.Response{StopReason: "end_turn"}, nil
}

func (c *callInfoClient) Stream(ctx context.Context, _ *model.Request) (model.ValidatedStreamer, error) {
	info, ok := model.CallInfoFrom(ctx)
	c.calls <- callInfoObservation{info: info, present: ok}
	return &wrapperLifecycleStream{response: &model.Response{StopReason: "end_turn"}}, nil
}

func (p callInfoPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{}, p.call(ctx, in.Agent)
}

func (p callInfoPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{}, p.call(ctx, in.Agent)
}

func (s failingSequenceStore) NextSeq(context.Context, string, string) (int, error) {
	return 0, s.err
}

func newCallInfoRuntime(t *testing.T, call func(context.Context, planner.PlannerContext) error) (*Runtime, *callInfoClient, *PlanActivityInput) {
	t.Helper()
	rt := New()
	client := &callInfoClient{calls: make(chan callInfoObservation, 20)}
	require.NoError(t, rt.RegisterModel("registered-model", client))
	rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: callInfoPlanner{call: call}}
	return rt, client, &PlanActivityInput{
		AgentID: "svc.agent", RunID: "child-run",
		RunContext: run.Context{RootRunID: "root-run", RunID: "child-run", SessionID: "session", TurnID: "turn", Attempt: 99},
	}
}
