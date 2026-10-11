package runtime

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallRecordingOverflowStopsAttempt(t *testing.T) {
	for _, tc := range []struct {
		source        string
		step          overflow.Step
		calibrated    bool
		providerError error
		want          error
		outcome       contextmeasure.RequestOutcome
	}{
		{"guard", overflow.Step{}, true, nil, ErrContextBudget, contextmeasure.OutcomePreSendRejected},
		{"no_progress", overflow.Step{Rejected: map[string]int{"m": 50}}, false, nil, ErrContextOverflowNoProgress, contextmeasure.OutcomeError},
		{"provider", overflow.Step{}, false, model.NewProviderError("test", "complete", 400, model.ProviderErrorKindContextOverflow, "", "overflow", "", false, nil), nil, contextmeasure.OutcomeOverflow},
	} {
		t.Run(tc.source, func(t *testing.T) {
			store := &recordingTestStore{Store: inmem.New()}
			provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: store, response: &model.Response{}, err: tc.providerError}, limits: model.ContextLimits{ContextWindow: 100}, resolved: "m"}
			view := []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("x", 600)}}}}
			r := NewCallRecording(store, "root", index.ViewRef{}, view, 1)
			r.configureOverflow(&PlanActivityInput{Overflow: &tc.step, Models: []api.ModelContext{{Model: "m", Rate: contextmeasure.Rate{Tokens: 1, Bytes: 3}, Calibrated: tc.calibrated}}})
			client := &callRecordingClient{inner: provider, recording: r}
			_, err := client.Complete(recordingCallContext(1), &model.Request{Model: "m", Messages: view})
			if tc.source == "provider" {
				require.ErrorIs(t, err, tc.providerError)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
			_, err = client.Complete(recordingCallContext(2), &model.Request{Model: "m", Messages: view})
			require.ErrorIs(t, err, ErrContextOverflowPending)
			require.NoError(t, r.Finish(t.Context()))
			calls, err := store.ListCalls(t.Context(), "root", "run")
			require.NoError(t, err)
			require.Len(t, calls, 1)
			assert.Equal(t, runstore.CallFailed, calls[0].State)
			completion, err := callrecord.DecodeCompletion(calls[0].Completion)
			require.NoError(t, err)
			assert.Equal(t, tc.outcome, completion.Facts.Outcome)
			report := r.Overflow()
			require.NotNil(t, report)
			assert.Equal(t, tc.source, report.Source)
			assert.Equal(t, 600, report.ViewTextBytes)
			assert.Equal(t, 1, report.Ordinal)
		})
	}
}
