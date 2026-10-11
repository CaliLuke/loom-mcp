package agenttest_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturePlanner struct {
	stream   bool
	behavior string
	entered  chan<- struct{}
	release  <-chan struct{}
}

func TestCaptureFirstRequest(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			h := agenttest.New(t, registerCaptureAgent(capturePlanner{stream: stream}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("done"))))
			r, err := h.CaptureFirstRequest(t.Context(), "root", agenttest.User("hello"))
			require.NoError(t, err)
			assert.Equal(t, "model", r.ModelID())
			source := r.Source()
			require.NotNil(t, source)
			assert.Equal(t, 1, source.Seq)
			assert.Equal(t, 1, source.Attempt)
			record, err := runrecord.Read(t.Context(), h.Runtime().RunStore, h.Runtime().RunEventStore, source.RunID, source.RunID)
			require.NoError(t, err)
			require.Len(t, record.Runs[0].Calls, 1)
			call := record.Runs[0].Calls[0]
			expected, err := agenttest.NewRequest(call.Started.ModelID, call.Request)
			require.NoError(t, err)
			capturedReq, err := r.Request()
			require.NoError(t, err)
			expectedReq, err := expected.Request()
			require.NoError(t, err)
			expectedBytes, err := callrecord.Encode(expectedReq)
			require.NoError(t, err)
			capturedBytes, err := callrecord.Encode(capturedReq)
			require.NoError(t, err)
			assert.Equal(t, expectedBytes, capturedBytes)
			assert.Equal(t, model.TextPart{Text: "capture system"}, capturedReq.Messages[0].Parts[0])
			require.Len(t, capturedReq.Tools, 2)
			assert.Equal(t, "runtime.tool_unavailable", capturedReq.Tools[1].Name)
			assert.Equal(t, stream, capturedReq.Stream)
			assert.Equal(t, 1, h.Script("model").Remaining())
			gated, ok := h.Runtime().ModelClient("model")
			require.True(t, ok)
			_, hasCounter := gated.(model.TokenCounter)
			assert.False(t, hasCounter)
			assert.Empty(t, h.ToolCalls())
			source.Seq = 999
			assert.Equal(t, 1, r.Source().Seq)
			data, err := agenttest.MarshalRequest(r)
			require.NoError(t, err)
			restored, err := agenttest.UnmarshalRequest(data)
			require.NoError(t, err)
			assert.Equal(t, r.Source(), restored.Source())
			result, err := h.Run(t.Context(), "root", agenttest.User("normal"))
			require.NoError(t, err)
			assert.NoError(t, result.RunErr)
			// A capture is still possible after registration closes on the first run.
			_, err = h.CaptureFirstRequest(t.Context(), "root", agenttest.User("again"))
			require.NoError(t, err)
		})
	}
}

func TestCaptureFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		behavior string
		want     error
	}{
		{"swallow", agenttest.ErrCaptureHandled}, {"retry", agenttest.ErrCaptureHandled}, {"none", agenttest.ErrCaptureNoModelCall},
	} {
		t.Run(tc.behavior, func(t *testing.T) {
			h := agenttest.New(t, registerCaptureAgent(capturePlanner{behavior: tc.behavior}), agenttest.ScriptedModel("model"))
			_, err := h.CaptureFirstRequest(t.Context(), "root", nil)
			require.ErrorIs(t, err, tc.want)
			if tc.behavior != "none" {
				assert.ErrorContains(t, err, "the planner handled the capture sentinel instead of returning it")
			}
		})
	}
}

func TestCaptureConcurrentWithRun(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	h := agenttest.New(t, registerCaptureAgent(capturePlanner{entered: entered, release: release}), agenttest.ScriptedModel("model", agenttest.Respond(agenttest.Text("normal"))))
	var wg sync.WaitGroup
	wg.Go(func() {
		r, err := h.CaptureFirstRequest(t.Context(), "root", agenttest.User("capture"))
		if assert.NoError(t, err) {
			assert.Equal(t, "model", r.ModelID())
		}
	})
	wg.Go(func() {
		r, err := h.Run(t.Context(), "root", agenttest.User("normal"))
		if assert.NoError(t, err) {
			assert.NoError(t, r.RunErr)
		}
	})
	<-entered
	<-entered
	close(release)
	wg.Wait()
	assert.Equal(t, 0, h.Script("model").Remaining())
}

func TestCaptureGateCapabilities(t *testing.T) {
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("done")))
	require.NoError(t, err)
	inner := model.WithCapabilities(script.Client(), model.TokenEstimator{}, script)
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("model", inner); err != nil {
			return err
		}
		return registerCaptureAgent(capturePlanner{})(ctx, rt)
	})
	gated, ok := h.Runtime().ModelClient("model")
	require.True(t, ok)
	counter, ok := gated.(model.TokenCounter)
	require.True(t, ok)
	_, err = counter.CountTokens(t.Context(), &model.Request{Messages: agenttest.User("hi")})
	require.NoError(t, err)
	_, ok = gated.(model.ContextInfo)
	assert.True(t, ok)
	assert.Equal(t, inner, h.Model("model"))
	ids := h.Runtime().ModelIDs()
	result, err := h.Run(t.Context(), "root", agenttest.User("hi"))
	require.NoError(t, err)
	assert.NoError(t, result.RunErr)
	assert.Equal(t, ids, h.Runtime().ModelIDs())
}

func registerCaptureAgent(p capturePlanner) agenttest.Register {
	return func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{echoSpec()}, Execute: func(context.Context, *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			return nil, fmt.Errorf("unexpected tool execution")
		}}); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "root", Planner: p, Specs: []tools.ToolSpec{echoSpec()}, Workflow: engine.WorkflowDefinition{Name: "capture.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "capture.plan", ResumeActivityName: "capture.resume", ExecuteToolActivity: "capture.tool"})
	}
}

func (p capturePlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	return p.capture(ctx, in.Agent, in.Messages)
}
func (p capturePlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.capture(ctx, in.Agent, in.Messages)
}
func (p capturePlanner) capture(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	if p.entered != nil {
		p.entered <- struct{}{}
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.behavior == "none" {
		return captureFinal(), nil
	}
	client, ok := pc.ModelClient("model")
	if !ok {
		return nil, fmt.Errorf("model missing")
	}
	req := &model.Request{Model: "provider", Stream: p.stream, Messages: append([]*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "capture system"}}}}, messages...), Tools: []*model.ToolDefinition{{Name: "echo.echo", InputSchema: map[string]any{"type": "object"}}}}
	var err error
	if p.stream {
		var stream model.ValidatedStreamer
		stream, err = client.Stream(ctx, req)
		if err == nil {
			_, err = planner.ConsumeStream(ctx, stream, planner.NoopEvents())
		}
	} else {
		_, err = client.Complete(ctx, req)
	}
	if err != nil {
		switch p.behavior {
		case "swallow":
			return captureFinal(), nil
		case "retry":
			_, err = client.Complete(ctx, req)
		}
		return nil, err
	}
	return captureFinal(), nil
}
func captureFinal() *planner.PlanResult {
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}}
}
