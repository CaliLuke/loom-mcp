package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

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

func TestRetryAndReflectInterceptorConvertsToolErrorToRetryHint(t *testing.T) {
	rt := New(WithInterceptors(NewRetryAndReflectInterceptor(RetryAndReflectConfig{MaxRetries: 2})))
	rt.toolsets["svc.tools"] = ToolsetRegistration{
		Name: "svc.tools",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return nil, errors.New("backend rejected limit")
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
	require.Equal(t, `tool "svc.tools.search" failed: backend rejected limit`, out.Error)
	require.NotNil(t, out.RetryHint)
	require.Equal(t, planner.RetryReasonInvalidArguments, out.RetryHint.Reason)
	require.Equal(t, tools.Ident("svc.tools.search"), out.RetryHint.Tool)
	require.True(t, out.RetryHint.RestrictToTool)
	require.Equal(t, map[string]any{"query": "loom", "limit": float64(-1)}, out.RetryHint.PriorInput)
	require.Contains(t, out.RetryHint.Message, "Retry svc.tools.search with corrected arguments")
	require.Contains(t, out.RetryHint.Message, "backend rejected limit")
}
