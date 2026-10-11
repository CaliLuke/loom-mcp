package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sendFirstClient deliberately reverses the production recorder's write/send order.
type sendFirstClient struct {
	model.Client
	recording *CallRecording
}

// attemptSequenceStore injects the formal fault into the real identity wrapper.
type attemptSequenceStore struct {
	runstore.Store
}

func TestCallCommitSendBeforeStartedFault(t *testing.T) {
	for _, faulty := range []bool{false, true} {
		t.Run(map[bool]string{false: "production_prevents_fault", true: "send_before_started"}[faulty], func(t *testing.T) {
			rt := New()
			store := &recordingTestStore{Store: rt.RunStore, failStarted: true}
			recorder := NewCallRecording(store, "root", index.ViewRef{}, nil, 1)
			provider := &recordingTestClient{store: store, response: &model.Response{StopReason: "end"}}
			var client model.Client = &callRecordingClient{inner: provider, recording: recorder}
			if faulty {
				client = &sendFirstClient{Client: provider, recording: recorder}
			}
			_, err := client.Complete(recordingCallContext(1), &model.Request{})
			require.ErrorContains(t, err, "started store failure")
			record, err := runrecord.Read(t.Context(), store, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			assert.Empty(t, record.Runs[0].Calls)
			if faulty {
				assert.Equal(t, 1, provider.sent, "SentImpliesRecorded is violated: sent request is absent from RunRecord")
			} else {
				assert.Zero(t, provider.sent, "the recorder refuses to send without stored started evidence")
			}
		})
	}
}

func TestCallCommitInterceptedRecordEventFault(t *testing.T) {
	for _, faulty := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical_path", true: "intercepted_record_event"}[faulty], func(t *testing.T) {
			intercepted := 0
			rt := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
				intercepted++
				return &BeforeEventDecision{Drop: true}, nil
			}}))
			recorder := NewCallRecording(rt.RunStore, "root", index.ViewRef{}, nil, 1)
			client := &callRecordingClient{inner: &historyCountingClient{completeText: "accepted"}, recording: recorder}
			for seq := 1; seq <= 2; seq++ {
				ctx := model.WithCallInfo(t.Context(), model.CallInfo{RootRunID: "root", RunID: "run", Seq: seq, Attempt: 1})
				_, err := client.Complete(ctx, &model.Request{})
				require.NoError(t, err)
			}
			step := hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, recorder.Keys(), api.WorkflowProtocolVersion, time.Unix(1, 0))
			input, err := hooks.EncodeToHookInput(step, "")
			require.NoError(t, err)
			if faulty {
				// Deliberately bypass record-event routing, then exercise real interception.
				require.NoError(t, rt.publishInterceptableHookEvent(t.Context(), input, step, input.Payload))
			} else {
				require.NoError(t, rt.hookActivity(t.Context(), input))
			}
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			require.Len(t, record.Runs[0].Calls, 2)
			for _, call := range record.Runs[0].Calls {
				if faulty {
					assert.Equal(t, runrecord.Abandoned, call.Class, "ClassificationCorrect breaks for the actually adopted producing and auxiliary calls")
				} else {
					assert.Equal(t, runrecord.Adopted, call.Class)
				}
			}
			assert.Equal(t, map[bool]int{false: 0, true: 1}[faulty], intercepted)
		})
	}
}

func TestCallCommitSequencePerAttemptFault(t *testing.T) {
	for _, faulty := range []bool{false, true} {
		t.Run(map[bool]string{false: "run_scoped_allocator", true: "seq_per_attempt"}[faulty], func(t *testing.T) {
			rt := New()
			var store runstore.Store = rt.RunStore
			if faulty {
				store = attemptSequenceStore{Store: store}
			}
			for attempt := 1; attempt <= 2; attempt++ {
				recorder := NewCallRecording(store, "root", index.ViewRef{}, nil, 1)
				client := &callInfoClientWrapper{store: store, info: model.CallInfo{RootRunID: "root", RunID: "run", Attempt: attempt}, inner: &callRecordingClient{recording: recorder, inner: &historyCountingClient{completeText: "response"}}}
				_, err := client.Complete(t.Context(), &model.Request{})
				require.NoError(t, err)
			}
			record, err := runrecord.Read(t.Context(), store, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			require.Len(t, record.Runs[0].Calls, 2)
			first, second := record.Runs[0].Calls[0].Record.Key, record.Runs[0].Calls[1].Record.Key
			assert.NotEqual(t, first.Attempt, second.Attempt)
			if faulty {
				assert.Equal(t, first.Seq, second.Seq, "KeysUnique breaks across actual recorded attempts")
			} else {
				assert.NotEqual(t, first.Seq, second.Seq)
			}
		})
	}
}

// K8 has no overflow producer. Its error exit structurally discards provisional
// presentations for every rejected planner attempt; the mutant commits early.
func TestCallCommitOverflowPresentationFault(t *testing.T) {
	for _, faulty := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected_attempt_prevents_commit", true: "overflow_commits_presentation"}[faulty], func(t *testing.T) {
			rt, _, input := newCallInfoRuntime(t, nil)
			overflow := errors.New("injected context overflow")
			rt.models["registered-model"] = &historyCountingClient{completeText: "provisional"}
			rt.agents[input.AgentID] = AgentRegistration{ID: input.AgentID, Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				client, ok := in.Agent.ModelClient("registered-model")
				require.True(t, ok)
				response, err := client.Complete(ctx, &model.Request{})
				if err != nil {
					return nil, err
				}
				events := in.Events.(*runtimePlannerEvents)
				id := events.StartModelPresentation(ctx)
				require.NoError(t, events.CommitModelPresentation(ctx, id, response))
				events.FinishModelPresentation(ctx, id, true)
				if faulty {
					// Deliberately commit before the planner's result is accepted.
					if err := events.commitModelPresentations(ctx); err != nil {
						return nil, err
					}
				}
				return nil, overflow
			}}}
			input.Step = 1
			_, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
			require.ErrorIs(t, err, overflow)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root-run", "child-run")
			require.NoError(t, err)
			require.Len(t, record.Runs[0].Calls, 1)
			assert.Equal(t, runrecord.Abandoned, record.Runs[0].Calls[0].Class)
			if faulty {
				assert.NotEmpty(t, record.Runs[0].Presentations, "NoPresentationFromOverflow breaks when the rejected attempt commits early")
			} else {
				assert.Empty(t, record.Runs[0].Presentations)
			}
		})
	}
}

// Complete sends before attempting the started write, the deliberately faulty order.
func (c *sendFirstClient) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	response, err := c.Client.Complete(ctx, request)
	if err != nil {
		return nil, err
	}
	if _, err := c.recording.begin(ctx, request, c.Client); err != nil {
		return nil, err
	}
	return response, nil
}

// NextSeq deliberately restarts the counter for each activity attempt.
func (s attemptSequenceStore) NextSeq(context.Context, string, string) (int, error) {
	return 1, nil
}
