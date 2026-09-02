package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestGenerateDeterministicToolCallID_UniqueAcrossAttempts(t *testing.T) {
	id1 := generateDeterministicToolCallID("run-1", "turn-1", 1, "svc.read.get_time_series", 0)
	id2 := generateDeterministicToolCallID("run-1", "turn-1", 2, "svc.read.get_time_series", 0)
	require.NotEqual(t, id1, id2)
}

func TestGenerateDeterministicToolCallID_DeterministicForSameInputs(t *testing.T) {
	id1 := generateDeterministicToolCallID("run-1", "turn-1", 3, "svc.read.get_time_series", 7)
	id2 := generateDeterministicToolCallID("run-1", "turn-1", 3, "svc.read.get_time_series", 7)
	require.Equal(t, id1, id2)
}

func TestResultsRequireRecoveryClassifiesCorrectableFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []*planner.ToolResult
		want    bool
	}{
		{
			name: "tool_unavailable_requires_one_replacement_turn",
			results: []*planner.ToolResult{{
				Name:       tools.Ident("svc.data.discover"),
				ToolCallID: "tc1",
				Error:      planner.NewToolError("no healthy providers"),
				RetryHint: &planner.RetryHint{
					Reason: planner.RetryReasonToolUnavailable,
					Tool:   tools.Ident("svc.data.discover"),
				},
			}},
			want: true,
		},
		{
			name: "rate_limited_counts",
			results: []*planner.ToolResult{{
				Name:       tools.Ident("svc.data.discover"),
				ToolCallID: "tc2",
				Error:      planner.NewToolError("rate limited"),
				RetryHint: &planner.RetryHint{
					Reason: planner.RetryReasonRateLimited,
					Tool:   tools.Ident("svc.data.discover"),
				},
			}},
			want: false,
		},
		{
			name: "no_hint_counts",
			results: []*planner.ToolResult{{
				Name:       tools.Ident("svc.data.discover"),
				ToolCallID: "tc3",
				Error:      planner.NewToolError("boom"),
			}},
			want: true,
		},
		{
			name: "unknown_retry_reason_counts_without_panic",
			results: []*planner.ToolResult{{
				Name:       tools.Ident("svc.data.discover"),
				ToolCallID: "tc4",
				Error:      planner.NewToolError("future retry reason"),
				RetryHint: &planner.RetryHint{
					Reason: planner.RetryReason("future_reason"),
					Tool:   tools.Ident("svc.data.discover"),
				},
			}},
			want: false,
		},
		{
			name: "later_invalid_arguments_requires_recovery",
			results: []*planner.ToolResult{
				{
					Name:       tools.Ident("svc.data.discover"),
					ToolCallID: "tc5",
					Error:      planner.NewToolError("rate limited"),
					RetryHint: &planner.RetryHint{
						Reason: planner.RetryReasonRateLimited,
						Tool:   tools.Ident("svc.data.discover"),
					},
				},
				{
					Name:       tools.Ident("svc.data.update"),
					ToolCallID: "tc6",
					Error:      planner.NewToolError("invalid arguments"),
					RetryHint: &planner.RetryHint{
						Reason: planner.RetryReasonInvalidArguments,
						Tool:   tools.Ident("svc.data.update"),
					},
				},
			},
			want: true,
		},
		{
			name: "later_unavailable_requires_recovery_after_unknown_reason",
			results: []*planner.ToolResult{
				{
					Name:       tools.Ident("svc.data.discover"),
					ToolCallID: "tc7",
					Error:      planner.NewToolError("future retry reason"),
					RetryHint: &planner.RetryHint{
						Reason: planner.RetryReason("future_reason"),
						Tool:   tools.Ident("svc.data.discover"),
					},
				},
				{
					Name:       tools.Ident("svc.data.update"),
					ToolCallID: "tc8",
					Error:      planner.NewToolError("unavailable"),
					RetryHint: &planner.RetryHint{
						Reason: planner.RetryReasonToolUnavailable,
						Tool:   tools.Ident("svc.data.update"),
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, resultsRequireRecovery(tt.results))
		})
	}
}
