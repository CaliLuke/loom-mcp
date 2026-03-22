package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
)

func TestTerminalRunStatusForEngineStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  engine.RunStatus
		want    string
		wantErr string
	}{
		{name: "completed", status: engine.RunStatusCompleted, want: runStatusSuccess},
		{name: "timed_out", status: engine.RunStatusTimedOut, want: runStatusFailed},
		{name: "failed", status: engine.RunStatusFailed, want: runStatusFailed},
		{name: "canceled", status: engine.RunStatusCanceled, want: runStatusCanceled},
		{name: "pending_errors", status: engine.RunStatusPending, wantErr: "non-terminal engine run status"},
		{name: "unknown_errors", status: engine.RunStatus("mystery"), wantErr: "unexpected engine run status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := terminalRunStatusForEngineStatus(tt.status)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTerminalRunErrorForStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		status           engine.RunStatus
		wantTerminalErr  error
		wantTerminalText string
		wantMappingErr   string
	}{
		{name: "completed", status: engine.RunStatusCompleted},
		{name: "timed_out", status: engine.RunStatusTimedOut, wantTerminalErr: context.DeadlineExceeded},
		{name: "failed", status: engine.RunStatusFailed, wantTerminalText: "workflow failed before runtime emitted RunCompleted"},
		{name: "canceled", status: engine.RunStatusCanceled, wantTerminalErr: context.Canceled},
		{name: "paused_errors", status: engine.RunStatusPaused, wantMappingErr: "non-terminal engine run status"},
		{name: "unknown_errors", status: engine.RunStatus("mystery"), wantMappingErr: "unexpected engine run status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := terminalRunErrorForStatus(tt.status)
			if tt.wantMappingErr != "" {
				require.ErrorContains(t, err, tt.wantMappingErr)
				require.NoError(t, got)
				return
			}
			require.NoError(t, err)
			if tt.wantTerminalErr != nil {
				require.ErrorIs(t, got, tt.wantTerminalErr)
				return
			}
			if tt.wantTerminalText != "" {
				require.ErrorContains(t, got, tt.wantTerminalText)
				return
			}
			require.NoError(t, got)
		})
	}
}
