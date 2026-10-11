package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresentationRecordsParallelCallKeys(t *testing.T) {
	rt := New()
	events := newPlannerEvents(rt, "agent", "run", "", "turn")
	ctx := model.WithCallInfo(t.Context(), model.CallInfo{RunID: "run", Seq: 7, Attempt: 2})
	first := events.StartModelPresentation(ctx)
	second := events.StartModelPresentation(t.Context())
	for _, id := range []string{first, second} {
		require.NoError(t, events.CommitModelPresentation(t.Context(), id, &model.Response{Content: []model.Message{*assistantCommitResult().FinalResponse.Message}}))
		events.FinishModelPresentation(t.Context(), id, true)
	}
	require.NoError(t, events.commitModelPresentations(t.Context()))
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	var committed hooks.AssistantTurnCommittedEvent
	require.NoError(t, json.Unmarshal(page.Events[0].Payload, &committed))
	assert.Equal(t, []string{first, second}, committed.PresentationIDs)
	assert.Equal(t, []*runstore.CallKey{{RunID: "run", Seq: 7, Attempt: 2}, nil}, committed.CallKeys)
}

func TestPlannerStepCanonicalAppendAndProjection(t *testing.T) {
	for _, appendFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "projection failure", true: "append failure"}[appendFails], func(t *testing.T) {
			failure := errors.New("store unavailable")
			log := &recordingRunlog{}
			if appendFails {
				log.err = failure
			}
			bus := hooks.NewBus()
			rt := New(WithHooks(bus), WithRunEventStore(log), WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
				t.Fatal("record event must bypass interception")
				return nil, nil
			}}))
			projected := 0
			_, err := rt.registerSubscriber(bus, hooks.SubscriberFunc(func(context.Context, hooks.Event) error {
				projected++
				return errors.New("projection unavailable")
			}), SubscriberCritical)
			require.NoError(t, err)
			event := hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, nil, api.WorkflowProtocolVersion, time.Unix(10, 0))
			err = rt.publishHook(t.Context(), event, "turn")
			if appendFails {
				require.ErrorIs(t, err, failure)
				assert.Zero(t, projected)
			} else {
				require.NoError(t, err)
				assert.Len(t, log.events, 1)
				assert.Equal(t, 1, projected)
			}
		})
	}
}
