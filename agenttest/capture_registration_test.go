package agenttest_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type registrationCaptureClient struct {
	calls atomic.Int32
}

type retainedCapturePlanner struct {
	capturePlanner
	client model.Client
}

func TestCaptureRegistrationRetainedInterceptorClient(t *testing.T) {
	for _, returned := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, override := range []bool{false, true} {
				t.Run(fmt.Sprintf("returned=%t/stream=%t/override=%t", returned, stream, override), func(t *testing.T) {
					live := &registrationCaptureClient{}
					var held model.Client
					interceptor := runtime.RuntimeInterceptorFuncs{BeforeModelFunc: func(ctx context.Context, in *runtime.BeforeModelInput) (*runtime.BeforeModelDecision, error) {
						if stream {
							_, err := held.Stream(ctx, in.Request)
							assert.ErrorIs(t, err, agenttest.ErrCaptureSentinel)
							return nil, nil
						}
						_, err := held.Complete(ctx, in.Request)
						assert.ErrorIs(t, err, agenttest.ErrCaptureSentinel)
						return nil, nil
					}}
					opts := []agenttest.Option{agenttest.WithRuntimeOptions(runtime.WithInterceptors(interceptor))}
					if override {
						opts = append(opts, agenttest.ScriptedModel("model"))
					}
					h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
						if returned {
							var err error
							held, err = rt.RegisterModelClient("model", live)
							if err != nil {
								return err
							}
							current, ok := rt.ModelClient("model")
							require.True(t, ok)
							assert.Equal(t, held, current)
						} else {
							if err := rt.RegisterModel("model", live); err != nil {
								return err
							}
							var ok bool
							held, ok = rt.ModelClient("model")
							require.True(t, ok)
						}
						return registerCaptureAgent(capturePlanner{stream: stream})(ctx, rt)
					}, opts...)

					request, err := h.CaptureFirstRequest(t.Context(), "root", agenttest.User("capture held client"))
					assert.Zero(t, live.calls.Load(), "retained registration client must not reach the provider")
					require.NoError(t, err)
					assert.Equal(t, "model", request.ModelID())
					require.NotNil(t, request.Source())
					assert.Equal(t, 1, request.Source().Seq)
				})
			}
		}
	}
}

func TestCaptureRegistrationRetainedPlannerClient(t *testing.T) {
	live := &registrationCaptureClient{}
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("model", live); err != nil {
			return err
		}
		held, ok := rt.ModelClient("model")
		require.True(t, ok)
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "root", Planner: retainedCapturePlanner{client: held}, Workflow: engine.WorkflowDefinition{Name: "held.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "held.plan", ResumeActivityName: "held.resume", ExecuteToolActivity: "held.tool"})
	})
	_, err := h.CaptureFirstRequest(t.Context(), "root", agenttest.User("direct planner"))
	assert.Zero(t, live.calls.Load())
	// Direct runtime clients do not record calls; the gate must still block them.
	assert.ErrorIs(t, err, agenttest.ErrCaptureNoModelCall)
}

func (p retainedCapturePlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	_, err := p.client.Complete(ctx, &model.Request{Model: "provider", Messages: in.Messages})
	return nil, err
}

func (c *registrationCaptureClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	c.calls.Add(1)
	return nil, errors.New("live provider reached")
}

func (c *registrationCaptureClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	c.calls.Add(1)
	return nil, errors.New("live provider reached")
}
