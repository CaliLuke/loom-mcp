package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

type panickingInterceptorError struct{}

func (*panickingInterceptorError) Error() string {
	panic("broken interceptor error")
}

func TestExecuteToolActivityRunsInterceptorsAroundTool(t *testing.T) {
	var calls []string
	var executorPayload map[string]string

	rt := New(
		WithInterceptors(ToolInterceptorFuncs{
			BeforeToolFunc: func(ctx context.Context, input *BeforeToolInput) (*BeforeToolDecision, error) {
				calls = append(calls, "before")
				require.Equal(t, tools.Ident("svc.tools.echo"), input.Call.Name)
				require.JSONEq(t, `{"text":"original"}`, string(input.Call.Payload))
				return &BeforeToolDecision{
					Payload: rawjson.Message([]byte(`{"text":"rewritten"}`)),
				}, nil
			},
			AfterToolFunc: func(ctx context.Context, input *AfterToolInput) (*AfterToolDecision, error) {
				calls = append(calls, "after")
				require.Equal(t, tools.Ident("svc.tools.echo"), input.Call.Name)
				require.Equal(t, "rewritten", input.Result.Result.(map[string]any)["text"])
				return &AfterToolDecision{
					Result: &planner.ToolResult{
						Name:       input.Result.Name,
						ToolCallID: input.Result.ToolCallID,
						Result:     map[string]any{"text": "after"},
					},
				}, nil
			},
		}),
	)
	rt.toolsets["svc.tools"] = ToolsetRegistration{
		Name: "svc.tools",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			calls = append(calls, "execute")
			require.NoError(t, json.Unmarshal(call.Payload.RawMessage(), &executorPayload))
			return Executed(&planner.ToolResult{
				Name:       call.Name,
				ToolCallID: call.ToolCallID,
				Result:     map[string]any{"text": executorPayload["text"]},
			}), nil
		},
	}
	rt.toolSpecs["svc.tools.echo"] = tools.ToolSpec{
		Name:    "svc.tools.echo",
		Toolset: "svc.tools",
		Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec},
		Result:  tools.TypeSpec{Codec: tools.AnyJSONCodec},
	}

	out, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "svc.tools",
		ToolName:    "svc.tools.echo",
		ToolCallID:  "call-1",
		Payload:     rawjson.Message([]byte(`{"text":"original"}`)),
	})

	require.NoError(t, err)
	require.Equal(t, []string{"before", "execute", "after"}, calls)
	require.Equal(t, map[string]string{"text": "rewritten"}, executorPayload)
	require.JSONEq(t, `{"text":"after"}`, string(out.Payload))
}

func TestExecuteToolActivityRunsAgentInterceptors(t *testing.T) {
	var calls []string

	rt := New(WithInterceptors(ToolInterceptorFuncs{
		BeforeToolFunc: func(ctx context.Context, input *BeforeToolInput) (*BeforeToolDecision, error) {
			calls = append(calls, "global-before")
			return nil, nil
		},
	}))
	rt.agents["svc.agent"] = AgentRegistration{
		ID: "svc.agent",
		Interceptors: []Interceptor{ToolInterceptorFuncs{
			BeforeToolFunc: func(ctx context.Context, input *BeforeToolInput) (*BeforeToolDecision, error) {
				calls = append(calls, "agent-before")
				require.Equal(t, "svc.agent", string(input.Call.AgentID))
				return &BeforeToolDecision{Payload: rawjson.Message([]byte(`{"text":"agent"}`))}, nil
			},
			AfterToolFunc: func(ctx context.Context, input *AfterToolInput) (*AfterToolDecision, error) {
				calls = append(calls, "agent-after")
				return nil, nil
			},
		}},
	}
	rt.toolsets["svc.tools"] = ToolsetRegistration{
		Name: "svc.tools",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			calls = append(calls, "execute")
			var result map[string]any
			require.NoError(t, json.Unmarshal(call.Payload.RawMessage(), &result))
			return Executed(&planner.ToolResult{
				Name:       call.Name,
				ToolCallID: call.ToolCallID,
				Result:     result,
			}), nil
		},
	}
	rt.toolSpecs["svc.tools.echo"] = tools.ToolSpec{
		Name:    "svc.tools.echo",
		Toolset: "svc.tools",
		Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec},
		Result:  tools.TypeSpec{Codec: tools.AnyJSONCodec},
	}

	out, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		AgentID:     "svc.agent",
		ToolsetName: "svc.tools",
		ToolName:    "svc.tools.echo",
		ToolCallID:  "call-1",
		Payload:     rawjson.Message([]byte(`{"text":"original"}`)),
	})

	require.NoError(t, err)
	require.Equal(t, []string{"global-before", "agent-before", "execute", "agent-after"}, calls)
	require.JSONEq(t, `{"text":"agent"}`, string(out.Payload))
}

func TestInterceptorsForAgentReturnsPrecomputedMergedSlice(t *testing.T) {
	global := ToolInterceptorFuncs{}
	local := ToolInterceptorFuncs{}
	rt := New(WithInterceptors(global))
	rt.agents["svc.agent"] = AgentRegistration{
		ID:                 "svc.agent",
		Interceptors:       []Interceptor{local},
		mergedInterceptors: []Interceptor{global, local},
	}

	first := rt.interceptorsForAgent("svc.agent")
	second := rt.interceptorsForAgent("svc.agent")

	require.Len(t, first, 2)
	require.Same(t, &first[0], &second[0])
	require.Same(t, &first[1], &second[1])
}

func TestExecuteToolActivityEmptyAfterToolDecisionPreservesExecutorError(t *testing.T) {
	execErr := errors.New("backend down")
	rt := New(WithInterceptors(ToolInterceptorFuncs{
		AfterToolFunc: func(ctx context.Context, input *AfterToolInput) (*AfterToolDecision, error) {
			require.ErrorIs(t, input.Err, execErr)
			return &AfterToolDecision{}, nil
		},
	}))
	rt.toolsets["svc.tools"] = ToolsetRegistration{
		Name: "svc.tools",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return nil, execErr
		},
	}
	rt.toolSpecs["svc.tools.search"] = tools.ToolSpec{
		Name:    "svc.tools.search",
		Toolset: "svc.tools",
		Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec},
		Result:  tools.TypeSpec{Codec: tools.AnyJSONCodec},
	}

	out, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "svc.tools",
		ToolName:    "svc.tools.search",
		ToolCallID:  "call-1",
		Payload:     rawjson.Message([]byte(`{"query":"loom"}`)),
	})

	require.Nil(t, out)
	require.ErrorIs(t, err, execErr)
}

func TestExecuteWorkflowAfterRunInterceptorCanClearError(t *testing.T) {
	planErr := errors.New("planner unavailable")
	afterOut := &RunOutput{
		AgentID: "svc.agent",
		RunID:   "run-1",
	}
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterRunFunc: func(ctx context.Context, input *AfterRunInput) (*AfterRunDecision, error) {
			require.ErrorIs(t, input.Err, planErr)
			return &AfterRunDecision{Output: afterOut}, nil
		},
	}))
	rt.agents["svc.agent"] = AgentRegistration{
		ID:                  "svc.agent",
		PlanActivityName:    "plan",
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}
	wfCtx := &routeWorkflowContext{
		ctx: context.Background(),
		plannerRoutes: map[string]func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error){
			"plan": func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error) {
				return &PlanActivityOutput{}, planErr
			},
		},
	}

	out, err := rt.ExecuteWorkflow(wfCtx, &RunInput{
		AgentID: "svc.agent",
		RunID:   "run-1",
		TurnID:  "turn-1",
	})

	require.NoError(t, err)
	require.Same(t, afterOut, out)
}

func TestExecuteWorkflowAfterRunInterceptorClearsCanceledStatus(t *testing.T) {
	recorder := &recordingHooks{}
	rt := New(WithHooks(recorder), WithInterceptors(RuntimeInterceptorFuncs{
		AfterRunFunc: func(ctx context.Context, input *AfterRunInput) (*AfterRunDecision, error) {
			require.ErrorIs(t, input.Err, context.Canceled)
			return &AfterRunDecision{
				Output: &RunOutput{
					AgentID: input.Input.AgentID,
					RunID:   input.Input.RunID,
				},
			}, nil
		},
	}))
	rt.agents["svc.agent"] = AgentRegistration{
		ID:                  "svc.agent",
		PlanActivityName:    "plan",
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}
	rt.RunEventStore = runloginmem.New()
	wfCtx := &routeWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
		plannerRoutes: map[string]func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error){
			"plan": func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error) {
				return &PlanActivityOutput{}, context.Canceled
			},
		},
	}

	out, err := rt.ExecuteWorkflow(wfCtx, &RunInput{
		AgentID: "svc.agent",
		RunID:   "run-1",
		TurnID:  "turn-1",
	})

	require.NoError(t, err)
	require.NotNil(t, out)
	var completed *hooks.RunCompletedEvent
	for _, evt := range recorder.events {
		if e, ok := evt.(*hooks.RunCompletedEvent); ok {
			completed = e
		}
	}
	require.NotNil(t, completed)
	require.Equal(t, runStatusSuccess, completed.Status)
}

func TestExecuteWorkflowPublishesCanceledStatus(t *testing.T) {
	recorder := &recordingHooks{}
	rt := New(WithHooks(recorder))
	rt.agents["svc.agent"] = AgentRegistration{
		ID:                  "svc.agent",
		PlanActivityName:    "plan",
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}
	rt.RunEventStore = runloginmem.New()
	wfCtx := &routeWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
		plannerRoutes: map[string]func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error){
			"plan": func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error) {
				return &PlanActivityOutput{}, context.Canceled
			},
		},
	}

	out, err := rt.ExecuteWorkflow(wfCtx, &RunInput{
		AgentID: "svc.agent",
		RunID:   "run-1",
		TurnID:  "turn-1",
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, out)
	var completed *hooks.RunCompletedEvent
	for _, evt := range recorder.events {
		if event, ok := evt.(*hooks.RunCompletedEvent); ok {
			completed = event
		}
	}
	require.NotNil(t, completed)
	require.Equal(t, runStatusCanceled, completed.Status)
}

func TestExecuteWorkflowSanitizesHostileCompletionError(t *testing.T) {
	recorder := &recordingHooks{}
	rt := New(WithHooks(recorder))
	rt.agents["svc.agent"] = AgentRegistration{
		ID:                  "svc.agent",
		PlanActivityName:    "plan",
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}
	rt.RunEventStore = runloginmem.New()
	cyclic := &cyclicCompletionError{}
	cyclic.next = cyclic
	wfCtx := &routeWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
		plannerRoutes: map[string]func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error){
			"plan": func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error) {
				return &PlanActivityOutput{}, cyclic
			},
		},
	}

	result := make(chan error, 1)
	go func() {
		_, err := rt.ExecuteWorkflow(wfCtx, &RunInput{
			AgentID: "svc.agent",
			RunID:   "run-hostile",
			TurnID:  "turn-hostile",
		})
		result <- err
	}()
	select {
	case err := <-result:
		require.ErrorIs(t, err, cancellation.ErrInvalidErrorGraph)
	case <-time.After(time.Second):
		t.Fatal("ExecuteWorkflow hung on a cyclic completion error")
	}

	var completed *hooks.RunCompletedEvent
	for _, event := range recorder.events {
		if candidate, ok := event.(*hooks.RunCompletedEvent); ok {
			completed = candidate
		}
	}
	require.NotNil(t, completed)
	require.Equal(t, runStatusFailed, completed.Status)
	require.EqualError(t, completed.Error, cancellation.ErrInvalidErrorGraph.Error())
}

func TestExecuteWorkflowSanitizesErrorsBetweenAfterRunInterceptors(t *testing.T) {
	cycle := &cyclicCompletionError{}
	cycle.next = cycle
	tests := []struct {
		name string
		err  error
	}{
		{name: "cycle", err: cycle},
		{name: "panicking error text", err: &panickingInterceptorError{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingHooks{}
			observedSecond := false
			rt := New(
				WithHooks(recorder),
				WithInterceptors(
					RuntimeInterceptorFuncs{AfterRunFunc: func(context.Context, *AfterRunInput) (*AfterRunDecision, error) {
						return &AfterRunDecision{Err: test.err}, nil
					}},
					RuntimeInterceptorFuncs{AfterRunFunc: func(_ context.Context, input *AfterRunInput) (*AfterRunDecision, error) {
						observedSecond = true
						require.ErrorIs(t, input.Err, cancellation.ErrInvalidErrorGraph)
						return nil, nil
					}},
				),
			)
			rt.agents["svc.agent"] = AgentRegistration{
				ID:                  "svc.agent",
				PlanActivityName:    "plan",
				ExecuteToolActivity: "execute",
				ResumeActivityName:  "resume",
			}
			rt.RunEventStore = runloginmem.New()
			wfCtx := &routeWorkflowContext{
				ctx:         context.Background(),
				hookRuntime: rt,
				plannerRoutes: map[string]func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error){
					"plan": func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error) {
						return &PlanActivityOutput{}, errors.New("planner failed")
					},
				},
			}

			out, err := rt.ExecuteWorkflow(wfCtx, &RunInput{
				AgentID: "svc.agent",
				RunID:   "run-hostile-interceptor",
				TurnID:  "turn-hostile-interceptor",
			})

			require.Nil(t, out)
			require.ErrorIs(t, err, cancellation.ErrInvalidErrorGraph)
			require.True(t, observedSecond)
			var completed *hooks.RunCompletedEvent
			for _, event := range recorder.events {
				if candidate, ok := event.(*hooks.RunCompletedEvent); ok {
					completed = candidate
				}
			}
			require.NotNil(t, completed)
			require.Equal(t, runStatusFailed, completed.Status)
			require.EqualError(t, completed.Error, cancellation.ErrInvalidErrorGraph.Error())
		})
	}
}

func TestAfterRunInterceptorReturnErrorIsSanitized(t *testing.T) {
	cycle := &cyclicCompletionError{}
	cycle.next = cycle
	_, err := runAfterRunInterceptors(
		context.Background(),
		[]Interceptor{RuntimeInterceptorFuncs{AfterRunFunc: func(context.Context, *AfterRunInput) (*AfterRunDecision, error) {
			return nil, cycle
		}}},
		RunInput{AgentID: "svc.agent", RunID: "run-hostile-interceptor-return"},
		run.Context{RunID: "run-hostile-interceptor-return"},
		nil,
		errors.New("planner failed"),
	)
	require.ErrorIs(t, err, cancellation.ErrInvalidErrorGraph)
}

func TestExecuteWorkflowEmptyAfterRunDecisionPreservesRunError(t *testing.T) {
	planErr := errors.New("planner unavailable")
	var observed error
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterRunFunc: func(ctx context.Context, input *AfterRunInput) (*AfterRunDecision, error) {
			observed = input.Err
			return &AfterRunDecision{}, nil
		},
	}))
	rt.agents["svc.agent"] = AgentRegistration{
		ID:                  "svc.agent",
		PlanActivityName:    "plan",
		ExecuteToolActivity: "execute",
		ResumeActivityName:  "resume",
	}
	wfCtx := &routeWorkflowContext{
		ctx: context.Background(),
		plannerRoutes: map[string]func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error){
			"plan": func(context.Context, *PlanActivityInput) (*PlanActivityOutput, error) {
				return &PlanActivityOutput{}, planErr
			},
		},
	}

	out, err := rt.ExecuteWorkflow(wfCtx, &RunInput{
		AgentID: "svc.agent",
		RunID:   "run-1",
		TurnID:  "turn-1",
	})

	require.ErrorIs(t, err, planErr)
	require.Nil(t, out)
	require.ErrorIs(t, observed, planErr)
}

func TestHookActivityEmptyAfterEventDecisionPreservesAppendError(t *testing.T) {
	appendErr := errors.New("append failed")
	var observed error
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterEventFunc: func(ctx context.Context, input *AfterEventInput) (*AfterEventDecision, error) {
			observed = input.Err
			return &AfterEventDecision{}, nil
		},
	}))
	rt.RunEventStore = &recordingRunlog{err: appendErr}

	input, err := hooks.EncodeToHookInput(hooks.NewPlannerNoteEvent("run-1", "svc.agent", "", "note", nil), "turn-1")
	require.NoError(t, err)

	err = rt.hookActivity(context.Background(), input)
	require.ErrorIs(t, err, appendErr)
	require.ErrorIs(t, observed, appendErr)
}

func TestRetryAndReflectInterceptorConvertsToolErrorToRetryHint(t *testing.T) {
	const secret = "submitted-secret-value"
	rt := New(WithInterceptors(NewRetryAndReflectInterceptor(RetryAndReflectConfig{MaxRetries: 2})))
	rt.toolsets["svc.tools"] = ToolsetRegistration{
		Name: "svc.tools",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return nil, errors.New("backend rejected submitted value " + secret)
		},
	}
	rt.toolSpecs["svc.tools.search"] = tools.ToolSpec{
		Name:    "svc.tools.search",
		Toolset: "svc.tools",
		Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec},
		Result:  tools.TypeSpec{Codec: tools.AnyJSONCodec},
	}

	out, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "svc.tools",
		ToolName:    "svc.tools.search",
		ToolCallID:  "call-1",
		Payload:     rawjson.Message([]byte(`{"query":"loom","limit":-1}`)),
	})

	require.NoError(t, err)
	require.Equal(t, "tool execution failed; retry with corrected arguments", out.Error)
	require.NotNil(t, out.RetryHint)
	require.Equal(t, planner.RetryReasonInvalidArguments, out.RetryHint.Reason)
	require.Equal(t, tools.Ident("svc.tools.search"), out.RetryHint.Tool)
	require.True(t, out.RetryHint.RestrictToTool)
	require.Nil(t, out.RetryHint.PriorInput)
	require.Contains(t, out.RetryHint.Message, "Retry svc.tools.search with corrected arguments")
	require.NotContains(t, out.Error, secret)
	require.NotContains(t, out.RetryHint.Message, secret)

	result := &planner.ToolResult{
		Name:       "svc.tools.search",
		ToolCallID: "call-1",
		Error:      planner.NewToolError(out.Error),
	}
	applyActivityRetryHint(result, rt.toolSpecs[result.Name], out)
	require.NotContains(t, result.Error.Error(), secret)
	require.NotContains(t, result.RetryHint.Message, secret)
	events, eventErr := rt.encodeToolEvents(context.Background(), []*planner.ToolResult{result})
	require.NoError(t, eventErr)
	require.Len(t, events, 1)
	require.NotContains(t, events[0].Error.Error(), secret)
	require.NotContains(t, events[0].RetryHint.Message, secret)
}
