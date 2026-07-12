package temporal

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func TestNewAgentDataConverter_RoundTripsToolResult(t *testing.T) {
	dc := NewAgentDataConverter(func(tools.Ident) (*tools.ToolSpec, bool) { return nil, false })
	_, err := dc.ToPayload(&planner.ToolResult{Name: "test.tool"})
	require.Error(t, err)
}

func TestNewAgentDataConverter_DecodesToolResultsSetIntoSinglePointer(t *testing.T) {
	toolName := tools.Ident("test.tool")
	dc := NewAgentDataConverter(func(tools.Ident) (*tools.ToolSpec, bool) { return nil, false })
	p, err := dc.ToPayload(&api.ToolResultsSet{
		RunID: "run-123",
		ID:    "await-123",
		Results: []*api.ProvidedToolResult{
			{
				Name:       toolName,
				ToolCallID: "tooluse-123",
				Result:     rawjson.Message([]byte(`{"value":"ok"}`)),
			},
		},
	})
	require.NoError(t, err)

	var decoded *api.ToolResultsSet
	require.NoError(t, dc.FromPayload(p, &decoded))
	require.NotNil(t, decoded)
	require.Len(t, decoded.Results, 1)
	require.Equal(t, toolName, decoded.Results[0].Name)
	require.JSONEq(t, `{"value":"ok"}`, string(decoded.Results[0].Result))
}

func TestNewAgentDataConverterRoundTripsRunOutput(t *testing.T) {
	t.Parallel()

	dc := NewAgentDataConverter(nil)
	want := &api.RunOutput{
		AgentID: "test.agent",
		RunID:   "run-123",
		Final: &model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: "done"}},
		},
		FinalToolResult: &api.ToolEvent{Name: "test.final", Result: rawjson.Message(`{"ok":true}`)},
		ToolEvents:      []*api.ToolEvent{{Name: "test.tool", Result: rawjson.Message(`{"value":1}`)}},
		Notes:           []*planner.PlannerAnnotation{{Text: "note"}},
		Usage:           &model.TokenUsage{InputTokens: 3, OutputTokens: 5},
	}
	payload, err := dc.ToPayload(want)
	require.NoError(t, err)
	var got *api.RunOutput
	require.NoError(t, dc.FromPayload(payload, &got))
	require.Equal(t, want, got)
}

func TestAgentDataConverterWireHelpersRejectNilBoundaryValues(t *testing.T) {
	t.Parallel()

	_, err := encodeRunOutputWire(nil)
	require.ErrorContains(t, err, "run output is nil")
	_, err = encodePlanActivityInputWire(nil)
	require.ErrorContains(t, err, "plan activity input is nil")
	_, err = encodeToolResultsSetWire(nil)
	require.ErrorContains(t, err, "tool results set is nil")
	var decoded *api.RunOutput
	require.ErrorContains(t, decodeRunOutput(nil, &decoded), "payload is nil")
}

func TestNewAgentDataConverter_RoundTripsPlanActivityInputToolOutputs(t *testing.T) {
	t.Parallel()

	dc := NewAgentDataConverter(func(tools.Ident) (*tools.ToolSpec, bool) { return nil, false })
	p, err := dc.ToPayload(&api.PlanActivityInput{
		AgentID: "test.agent",
		RunID:   "run-123",
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
				},
			},
		},
		RunContext: run.Context{
			RunID:   "run-123",
			Attempt: 2,
		},
		ToolOutputs: []*api.ToolCallOutput{
			{
				Name:       "test.tool",
				ToolCallID: "call-1",
				Payload:    rawjson.Message([]byte(`{"input":"ok"}`)),
				Result:     rawjson.Message([]byte(`{"output":"ok"}`)),
				ServerData: rawjson.Message([]byte(`[{"kind":"evidence"}]`)),
			},
		},
		TypedInputs: []planner.TypedInputOutput{
			{
				ID:      "approval",
				Payload: rawjson.Message([]byte(`{"approved":true}`)),
			},
		},
		Finalize: &planner.Termination{
			Reason:  planner.TerminationReasonToolCap,
			Message: "tool budget exhausted",
		},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"test.tool", "memory.lookup"},
		PolicyCaps: policy.CapsState{
			MaxToolCalls:                        5,
			RemainingToolCalls:                  3,
			MaxConsecutiveFailedToolCalls:       2,
			RemainingConsecutiveFailedToolCalls: 1,
		},
	})
	require.NoError(t, err)

	var decoded *api.PlanActivityInput
	require.NoError(t, dc.FromPayload(p, &decoded))
	require.NotNil(t, decoded)
	require.Len(t, decoded.ToolOutputs, 1)
	require.Equal(t, tools.Ident("test.tool"), decoded.ToolOutputs[0].Name)
	require.Equal(t, "call-1", decoded.ToolOutputs[0].ToolCallID)
	require.JSONEq(t, `{"input":"ok"}`, string(decoded.ToolOutputs[0].Payload))
	require.JSONEq(t, `{"output":"ok"}`, string(decoded.ToolOutputs[0].Result))
	require.JSONEq(t, `[{"kind":"evidence"}]`, string(decoded.ToolOutputs[0].ServerData))
	require.Len(t, decoded.TypedInputs, 1)
	require.Equal(t, "approval", decoded.TypedInputs[0].ID)
	require.JSONEq(t, `{"approved":true}`, string(decoded.TypedInputs[0].Payload))
	require.NotNil(t, decoded.Finalize)
	require.Equal(t, planner.TerminationReasonToolCap, decoded.Finalize.Reason)
	require.Equal(t, "tool budget exhausted", decoded.Finalize.Message)
	require.True(t, decoded.ToolPolicyActive)
	require.Equal(t, []tools.Ident{"test.tool", "memory.lookup"}, decoded.AllowedTools)
	require.Equal(t, policy.CapsState{
		MaxToolCalls:                        5,
		RemainingToolCalls:                  3,
		MaxConsecutiveFailedToolCalls:       2,
		RemainingConsecutiveFailedToolCalls: 1,
	}, decoded.PolicyCaps)
}

func TestNewAgentDataConverter_RejectsJSONStringifiedToolResult(t *testing.T) {
	dc := NewAgentDataConverter(func(tools.Ident) (*tools.ToolSpec, bool) { return nil, false })
	_, err := dc.ToPayload(planner.ToolResult{Name: "test.tool", Result: `{"value":"ok"}`})
	require.Error(t, err)
}
