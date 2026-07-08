package bedrock

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
)

type failingLedgerSource struct {
	err error
}

func (f failingLedgerSource) Messages(context.Context, string) ([]*model.Message, error) {
	return nil, f.err
}

func TestPrepareRequestReturnsLedgerMergeError(t *testing.T) {
	t.Parallel()

	ledgerErr := errors.New("ledger unavailable")
	client := &Client{
		defaultModel: "test-model",
		think:        defaultThinkingBudget,
		ledger:       failingLedgerSource{err: ledgerErr},
		logger:       telemetry.NewNoopLogger(),
	}

	_, err := client.prepareRequest(context.Background(), &model.Request{
		RunID: "run-123",
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}},
	})

	require.ErrorIs(t, err, ledgerErr)
	require.ErrorContains(t, err, "bedrock: merge ledger messages for run run-123")
}
