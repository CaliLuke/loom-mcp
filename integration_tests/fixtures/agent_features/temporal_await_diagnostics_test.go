package agentfeatures_test

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/encoding/protojson"
)

// logTemporalAwaitFailure distinguishes workflow failures from missing projections.
// It runs before the fixture's client and worker are stopped.
func logTemporalAwaitFailure(t *testing.T, temporalClient client.Client, fx *featureRuntime, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	page, err := fx.rt.RunEventStore.List(ctx, runID, "", 1000)
	if err != nil {
		t.Logf("await diagnostic: read run log: %v", err)
	} else {
		for _, event := range page.Events {
			t.Logf("await diagnostic run log: %s %s %s", event.Timestamp, event.Type, event.Payload)
		}
	}
	logTemporalHistory(t, temporalClient, runID)
}

func logTemporalHistory(t *testing.T, temporalClient client.Client, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	history := temporalClient.GetWorkflowHistory(ctx, runID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for history.HasNext() {
		event, err := history.Next()
		if err != nil {
			t.Logf("await diagnostic: read history: %v", err)
			return
		}
		data, err := protojson.Marshal(event)
		if err != nil {
			t.Logf("await diagnostic: encode history event: %v", err)
			return
		}
		t.Logf("await diagnostic history: %s", data)
	}
}
