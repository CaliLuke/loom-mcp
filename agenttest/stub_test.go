package agenttest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func echoTool() tools.TypedTool[any, any] {
	return tools.TypedTool[any, any]{Name: "echo.echo", Payload: tools.AnyJSONCodec, Result: tools.AnyJSONCodec}
}

func TestStubFIFOAndHandle(t *testing.T) {
	tool := echoTool()
	h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model",
			agenttest.Respond(agenttest.ToolCall[any, any](tool, map[string]any{"value": "one"})),
			agenttest.Respond(agenttest.ToolCall[any, any](tool, map[string]any{"value": "two"})).When(agenttest.LastToolResult(tool, "first")),
			agenttest.Respond(agenttest.Text("done")).When(agenttest.LastToolResult(tool, "two"))))
	stub := agenttest.Stub(h, tool)
	stub.Return(map[string]any{"value": "first"})
	stub.Handle(func(_ context.Context, p any) (any, error) {
		return p, nil
	})
	result, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	calls := stub.Calls()
	require.Len(t, calls, 2)
	assert.Equal(t, 1, calls[0].Response)
	assert.Equal(t, 2, calls[1].Response)
	assert.Equal(t, result.RunID, calls[0].RunID)
	assert.NotEmpty(t, calls[0].ToolCallID)
	assert.JSONEq(t, `{"value":"one"}`, string(calls[0].Raw))
	assert.Equal(t, map[string]any{"value": "two"}, calls[1].Result)
	require.Len(t, h.ToolCalls(), 2)
	expect.Require(t, expect.Trajectory(result.Trajectory), expect.Completed())
}

func TestStubToolErrorsContinue(t *testing.T) {
	for _, handle := range []bool{false, true} {
		t.Run(map[bool]string{false: "Fail", true: "Handle"}[handle], func(t *testing.T) {
			h := agenttest.New(t, registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
				agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))))
			stub := agenttest.Stub(h, echoTool())
			failure := errors.New("tool broke")
			if handle {
				stub.Handle(func(context.Context, any) (any, error) {
					return "must be discarded", failure
				})
			} else {
				stub.Fail(failure)
			}
			result, err := h.Run(t.Context(), "root", nil)
			require.NoError(t, err)
			require.NoError(t, result.RunErr)
			expect.Require(t, expect.Trajectory(result.Trajectory), expect.Completed(), expect.Failed(echoTool(), nil, func(e *planner.ToolError) error {
				assert.Contains(t, e.Error(), "tool broke")
				return nil
			}))
			assert.ErrorIs(t, stub.Calls()[0].Err, failure)
		})
	}
}

func TestStubStrictAndExhausted(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unstubbed", true: "exhausted"}[exhausted], func(t *testing.T) {
			turns := []agenttest.Turn{agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{"value":"bad"}`)))}
			if exhausted {
				turns = append(turns, turns[0])
			}
			turns = append(turns, agenttest.Respond(agenttest.Text("done")))
			h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}), agenttest.ScriptedModel("model", turns...))
			require.NoError(t, err)
			if exhausted {
				agenttest.Stub(h, echoTool()).Return("first")
			}
			result, err := h.Run(t.Context(), "root", nil)
			var failure *agenttest.StubError
			require.ErrorAs(t, err, &failure)
			require.NotNil(t, result)
			require.NoError(t, result.RunErr)
			expect.Require(t, expect.Trajectory(result.Trajectory), expect.Failed(echoTool(), nil, nil))
			assert.Equal(t, tools.Ident("echo.echo"), failure.Tool)
			assert.Equal(t, 1+map[bool]int{true: 1}[exhausted], failure.Call)
			assert.Equal(t, result.RunID, failure.RunID)
			assert.NotEmpty(t, failure.ToolCallID)
			assert.JSONEq(t, `{"value":"bad"}`, string(failure.Args))
			if exhausted {
				require.ErrorIs(t, failure, agenttest.ErrStubExhausted)
			}
			require.ErrorContains(t, h.Close(), failure.Error())
			require.Len(t, h.ToolCalls(), failure.Call)
		})
	}
}

func TestStubDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mode         runtime.DispatchMode
		unregistered bool
		duplicate    bool
		bounded      bool
		message      string
	}{
		{name: "inline", mode: runtime.DispatchInline, message: "inline/agent-as-tool not stubbable"},
		{name: "child", mode: runtime.DispatchAgentChild, message: "inline/agent-as-tool not stubbable"},
		{name: "unknown", unregistered: true, message: "not registered"},
		{name: "duplicate", duplicate: true, message: "stubbed twice"},
		{name: "bounded", bounded: true, message: "requires Bounds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := agenttest.NewHarness(t.Context(), func(_ context.Context, rt *runtime.Runtime) error {
				if tc.unregistered {
					return nil
				}
				spec := echoSpec()
				if tc.bounded {
					spec.Bounds = &tools.BoundsSpec{}
				}
				return rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{spec}, DispatchMode: tc.mode,
					Execute: func(context.Context, *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
						return nil, errors.New("should not run")
					}})
			})
			require.NoError(t, err)
			stub := agenttest.Stub(h, echoTool())
			if tc.bounded {
				stub.Return("bounded")
			}
			if tc.duplicate {
				agenttest.Stub(h, echoTool())
			}
			result, err := h.Run(t.Context(), "root", nil)
			assert.Nil(t, result)
			require.ErrorContains(t, err, tc.message)
			assert.ErrorContains(t, h.Close(), tc.message)
		})
	}
}

func TestStubDecodeFailure(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{echoSpec()}}),
		agenttest.ScriptedModel("model", agenttest.Respond(agenttest.RawToolCall("echo.echo", rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("done"))))
	require.NoError(t, err)
	tool := echoTool()
	tool.Payload.FromJSON = func([]byte) (any, error) {
		return nil, errors.New("descriptor decode broke")
	}
	agenttest.Stub(h, tool).Return("unused")
	result, err := h.Run(t.Context(), "root", nil)
	var failure *agenttest.StubError
	require.ErrorAs(t, err, &failure)
	require.NotNil(t, result)
	require.NoError(t, result.RunErr)
	assert.Contains(t, failure.Reason, "descriptor decode broke")
	assert.ErrorContains(t, h.Close(), "descriptor decode broke")
}

func TestStubRejectedResultMatchesExecutor(t *testing.T) {
	spec := echoSpec()
	spec.Result.Codec.ToJSON = func(any) ([]byte, error) {
		return nil, errors.New("result codec rejected value")
	}
	execute := func(_ context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
		return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: "bad"}), nil
	}
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := registerHarnessAgent("root", harnessPlanner{id: "model", specs: []tools.ToolSpec{spec}})(ctx, rt); err != nil {
			return err
		}
		return rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Specs: []tools.ToolSpec{spec}, Execute: execute})
	}, agenttest.ScriptedModel("model",
		agenttest.Respond(agenttest.RawToolCall(spec.Name, rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("real done")),
		agenttest.Respond(agenttest.RawToolCall(spec.Name, rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("stub done"))))
	h.Passthrough(echoTool())
	real, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, real.RunErr)
	agenttest.Stub(h, echoTool()).Return("bad")
	stub, err := h.Run(t.Context(), "root", nil)
	require.NoError(t, err)
	require.NoError(t, stub.RunErr)
	realFailure := recordedToolError(t, real)
	stubFailure := recordedToolError(t, stub)
	assert.Equal(t, realFailure, stubFailure)
	require.NotNil(t, stubFailure.Cause)
	assert.Contains(t, stubFailure.Cause.Error(), "result codec rejected value")
	expect.Require(t, expect.Trajectory(stub.Trajectory), expect.Failed(echoTool(), nil, nil), expect.Completed())
}

func recordedToolError(t *testing.T, result *agenttest.Result) *planner.ToolError {
	t.Helper()
	for _, step := range result.Trajectory.Steps {
		if step.Tool != nil && step.Tool.Error != nil {
			return step.Tool.Error
		}
	}
	t.Fatal("no failed tool step")
	return nil
}
