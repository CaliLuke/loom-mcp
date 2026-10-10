package temporal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
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
		ToolEventsRef:   "sha256:events",
		ToolEventCount:  1,
		ToolSummary:     api.ToolEventSummary{FailedCount: 1, LastError: planner.NewToolError("failed")},
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

func TestNewAgentDataConverter_RoundTripsPlanActivityView(t *testing.T) {
	t.Parallel()

	dc := NewAgentDataConverter(func(tools.Ident) (*tools.ToolSpec, bool) { return nil, false })
	p, err := dc.ToPayload(&api.PlanActivityInput{
		CallFilter:           api.PlanToolFilter{RestrictToTool: "test.tool", AllowedTags: []string{"read"}},
		FinalizeMaxToolCalls: 3,
		AgentID:              "test.agent",
		RunID:                "run-123",
		View:                 index.ViewRef{IndexHead: "sha256:head", Pending: []index.MessageEntry{{ID: 9, Kind: index.TypedInput, Hash: "sha256:answer", InputID: "approval", Chain: "sha256:chain"}}},
		RunContext: run.Context{
			RunID:   "run-123",
			Attempt: 2,
		},
		Finalize: &planner.Termination{
			Reason:  planner.TerminationReasonToolCap,
			Message: "tool budget exhausted",
		},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"test.tool", "memory.lookup"},
		PolicyCaps: policy.CapsState{
			MaxToolCalls:           5,
			RemainingToolCalls:     3,
			MaxRecoveryTurns:       2,
			RemainingRecoveryTurns: 1,
		},
		Recovery: &api.ModelRecovery{
			Kind:          model.OutputValidationOutputBounds,
			ByteCount:     8192,
			Usage:         model.TokenUsage{InputTokens: 11, OutputTokens: 17},
			Attempt:       3,
			CorrectionRef: "sha256:correction",
			DisableTools:  true,
			ToolCatalog:   []tools.Ident{"test.tool"},
		},
	})
	require.NoError(t, err)

	var decoded *api.PlanActivityInput
	require.NoError(t, dc.FromPayload(p, &decoded))
	require.NotNil(t, decoded)
	require.Equal(t, api.PlanToolFilter{RestrictToTool: "test.tool", AllowedTags: []string{"read"}}, decoded.CallFilter)
	require.Equal(t, 3, decoded.FinalizeMaxToolCalls)
	require.Equal(t, runstore.Hash("sha256:head"), decoded.View.IndexHead)
	require.Len(t, decoded.View.Pending, 1)
	require.Equal(t, "approval", decoded.View.Pending[0].InputID)
	require.Equal(t, runstore.Hash("sha256:answer"), decoded.View.Pending[0].Hash)
	require.NotNil(t, decoded.Finalize)
	require.Equal(t, planner.TerminationReasonToolCap, decoded.Finalize.Reason)
	require.Equal(t, "tool budget exhausted", decoded.Finalize.Message)
	require.True(t, decoded.ToolPolicyActive)
	require.Equal(t, []tools.Ident{"test.tool", "memory.lookup"}, decoded.AllowedTools)
	require.Equal(t, policy.CapsState{
		MaxToolCalls:           5,
		RemainingToolCalls:     3,
		MaxRecoveryTurns:       2,
		RemainingRecoveryTurns: 1,
	}, decoded.PolicyCaps)
	require.Equal(t, &api.ModelRecovery{
		Kind:          model.OutputValidationOutputBounds,
		ByteCount:     8192,
		Usage:         model.TokenUsage{InputTokens: 11, OutputTokens: 17},
		Attempt:       3,
		CorrectionRef: "sha256:correction",
		DisableTools:  true,
		ToolCatalog:   []tools.Ident{"test.tool"},
	}, decoded.Recovery)
}

func TestNewAgentDataConverterMigratesHistoricalFailureCaps(t *testing.T) {
	t.Parallel()

	dc := NewAgentDataConverter(nil)
	payload := &commonpb.Payload{
		Metadata: map[string][]byte{converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON)},
		Data: []byte(`{
		"AgentID":"test.agent",
		"RunID":"run-legacy",
		"PolicyCaps":{
			"MaxToolCalls":5,
			"RemainingToolCalls":2,
			"MaxConsecutiveFailedToolCalls":4,
			"RemainingConsecutiveFailedToolCalls":3,
			"ExpiresAt":"2027-01-02T03:04:05Z"
		}
	}`),
	}

	var decoded *api.PlanActivityInput
	require.NoError(t, dc.FromPayload(payload, &decoded))
	require.Equal(t, policy.CapsState{
		MaxToolCalls:           5,
		RemainingToolCalls:     2,
		MaxRecoveryTurns:       4,
		RemainingRecoveryTurns: 3,
		//lint:ignore SA1019 This test pins migration of the deprecated historical wire field.
		ExpiresAt: time.Date(2027, time.January, 2, 3, 4, 5, 0, time.UTC),
	}, decoded.PolicyCaps)
}

func TestNewAgentDataConverterMigratesHistoricalRunAndActivityOutputCaps(t *testing.T) {
	t.Parallel()

	dc := NewAgentDataConverter(nil)
	historicalRun := &commonpb.Payload{
		Metadata: map[string][]byte{converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON)},
		Data: []byte(`{
			"AgentID":"test.agent",
			"RunID":"run-legacy",
			"Policy":{"MaxToolCalls":7,"MaxConsecutiveFailedToolCalls":5}
		}`),
	}
	var runInput *api.RunInput
	require.NoError(t, dc.FromPayload(historicalRun, &runInput))
	require.NotNil(t, runInput.Policy)
	require.Equal(t, 7, runInput.Policy.MaxToolCalls)
	require.Equal(t, 5, runInput.Policy.MaxRecoveryTurns)

	historicalOutput := &commonpb.Payload{
		Metadata: map[string][]byte{converter.MetadataEncoding: []byte(converter.MetadataEncodingJSON)},
		Data: []byte(`{
			"Result":null,
			"PolicyCaps":{
				"MaxToolCalls":7,
				"RemainingToolCalls":4,
				"MaxConsecutiveFailedToolCalls":5,
				"RemainingConsecutiveFailedToolCalls":2
			}
		}`),
	}
	var output *api.PlanActivityOutput
	require.NoError(t, dc.FromPayload(historicalOutput, &output))
	require.Equal(t, policy.CapsState{
		MaxToolCalls:           7,
		RemainingToolCalls:     4,
		MaxRecoveryTurns:       5,
		RemainingRecoveryTurns: 2,
	}, output.PolicyCaps)
}

func TestNewAgentDataConverterRoundTripsRunPolicyDurations(t *testing.T) {
	t.Parallel()

	dc := NewAgentDataConverter(nil)
	want := &api.RunInput{
		AgentID: "test.agent",
		RunID:   "run-policy-durations",
		Policy: &api.PolicyOverrides{
			MaxRecoveryTurns: 2,
			TimeBudget:       2 * time.Second,
			PlanTimeout:      3 * time.Second,
			ToolTimeout:      4 * time.Second,
			PerToolTimeout: map[tools.Ident]time.Duration{
				"svc.read": 5 * time.Second,
			},
			FinalizerGrace:    6 * time.Second,
			InterruptsAllowed: true,
		},
	}
	payload, err := dc.ToPayload(want)
	require.NoError(t, err)

	var got *api.RunInput
	require.NoError(t, dc.FromPayload(payload, &got))
	require.Equal(t, want, got)
}

func TestNewAgentDataConverter_RejectsJSONStringifiedToolResult(t *testing.T) {
	dc := NewAgentDataConverter(func(tools.Ident) (*tools.ToolSpec, bool) { return nil, false })
	_, err := dc.ToPayload(planner.ToolResult{Name: "test.tool", Result: `{"value":"ok"}`})
	require.Error(t, err)
}
