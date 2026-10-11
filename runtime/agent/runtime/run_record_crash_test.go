package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type crashCallStore struct {
	runstore.Store
	window string
}

type crashRunLog struct {
	runlog.Store
	window string
}

type crashProvider struct {
	window string
	sent   int
}

type zombieProvider struct {
	sent    chan struct{}
	release chan struct{}
}

func TestRunRecordCommitCrashWindows(t *testing.T) {
	for _, test := range []struct {
		window string
		class  runrecord.CallClass
		sent   bool
		sealed bool
	}{
		{window: "before_started"},
		{window: "after_started_before_send", class: runrecord.Incomplete},
		{window: "after_send", class: runrecord.Incomplete, sent: true},
		{window: "after_completion_before_return", class: runrecord.Abandoned, sent: true},
		{window: "return_lost", class: runrecord.Abandoned, sent: true},
		{window: "before_planner_step", class: runrecord.Abandoned, sent: true},
		{window: "after_planner_step", class: runrecord.Adopted, sent: true},
		{window: "before_run_sealed", class: runrecord.Adopted, sent: true},
		{window: "after_run_sealed", class: runrecord.Adopted, sent: true, sealed: true},
	} {
		t.Run(test.window, func(t *testing.T) {
			rt := New()
			rt.RunStore = crashCallStore{Store: rt.RunStore, window: test.window}
			rt.RunEventStore = crashRunLog{Store: rt.RunEventStore, window: test.window}
			provider := &crashProvider{window: test.window}
			recorder := NewCallRecording(rt.RunStore, "root", index.ViewRef{}, nil, 1)
			client := &callRecordingClient{inner: provider, recording: recorder}
			key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
			// Fake engine delivery deliberately loses the successful activity result in
			// the return_lost window. Store/log hooks crash exactly at durable boundaries.
			runUntilCrash(func() error {
				response, err := client.Complete(recordingCallContext(1), &model.Request{Model: "fake"})
				if err != nil {
					return err
				}
				if test.window == "return_lost" {
					return errors.New("engine lost return")
				}
				event := hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, []runstore.CallKey{key}, api.WorkflowProtocolVersion, time.Unix(1, 0))
				input, err := hooks.EncodeToHookInput(event, "")
				if err != nil {
					return err
				}
				if err := rt.hookActivity(t.Context(), input); err != nil {
					return err
				}
				data, err := callrecord.Encode([]*model.Message{&response.Content[0]})
				if err != nil {
					return err
				}
				hash, err := rt.RunStore.Put(t.Context(), "root", data)
				if err != nil {
					return err
				}
				segment, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.Assistant, Hash: hash, Call: &key}})
				if err != nil {
					return err
				}
				_, err = rt.SealRecordActivity(t.Context(), &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", Pending: segment.Entries, EventKey: "run_sealed:run", Timestamp: time.Unix(2, 0)})
				return err
			})
			assert.Equal(t, test.sent, provider.sent > 0, "SentImpliesRecorded")
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			assert.Equal(t, test.sealed, record.Runs[0].Sealed)
			if test.class == "" {
				assert.Empty(t, record.Runs[0].Calls)
			} else {
				require.Len(t, record.Runs[0].Calls, 1)
				assert.Equal(t, test.class, record.Runs[0].Calls[0].Class, "ClassificationCorrect")
				assert.Equal(t, 1, record.Runs[0].Calls[0].Step)
			}
			if test.sealed {
				require.Len(t, record.Runs[0].Entries, 1)
				assert.Equal(t, &key, record.Runs[0].Entries[0].Call, "IndexMatchesAdoption")
			} else {
				assert.Empty(t, record.Runs[0].Entries, "orphan segment is not adopted")
			}
		})
	}
}

func TestRunRecordFailingAttemptCommitsNoPresentation(t *testing.T) {
	rt, _, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{})
		if err != nil {
			return err
		}
		return errors.New("planner attempt rejected after response")
	})
	rt.models["registered-model"] = &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "not accepted"}}}}, StopReason: "end_turn"}}
	input.Step = 1
	_, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.Error(t, err)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root-run", "child-run")
	require.NoError(t, err)
	assert.Empty(t, record.Runs[0].Presentations, "NoPresentationFromOverflow: failing-attempt subset; full overflow is K14")
	require.Len(t, record.Runs[0].Calls, 1)
	assert.Equal(t, runrecord.Abandoned, record.Runs[0].Calls[0].Class)
}

func TestRunRecordZombieAfterAdoption(t *testing.T) {
	rt := New()
	provider := &zombieProvider{sent: make(chan struct{}), release: make(chan struct{})}
	recorder := NewCallRecording(rt.RunStore, "root", index.ViewRef{}, nil, 1)
	timedOut, cancel := context.WithTimeout(recordingCallContext(1), 5*time.Millisecond)
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		wrapper := &callRecordingClient{inner: provider, recording: recorder}
		_, err := wrapper.Complete(timedOut, &model.Request{})
		completed <- err
	}()
	<-provider.sent
	<-timedOut.Done() // fake engine gives up; the provider deliberately ignores it
	require.ErrorIs(t, timedOut.Err(), context.DeadlineExceeded)
	winner := NewCallRecording(rt.RunStore, "root", index.ViewRef{}, nil, 1)
	winnerContext := model.WithCallInfo(t.Context(), model.CallInfo{RunID: "run", RootRunID: "root", Seq: 2, Attempt: 2})
	winnerClient := &callRecordingClient{inner: &historyCountingClient{completeText: "winner"}, recording: winner}
	_, err := winnerClient.Complete(winnerContext, &model.Request{})
	require.NoError(t, err)
	key := runstore.CallKey{RunID: "run", Seq: 2, Attempt: 2}
	event := hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, []runstore.CallKey{key}, api.WorkflowProtocolVersion, time.Unix(1, 0))
	input, err := hooks.EncodeToHookInput(event, "")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(t.Context(), input))
	before, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	assert.Equal(t, runrecord.Incomplete, before.Runs[0].Calls[0].Class)
	assert.Equal(t, runrecord.Adopted, before.Runs[0].Calls[1].Class)
	close(provider.release)
	require.NoError(t, <-completed)
	after, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	assert.Equal(t, runrecord.Abandoned, after.Runs[0].Calls[0].Class)
	assert.Equal(t, 1, after.Runs[0].Calls[0].Step)
}

func (s crashCallStore) PutCall(ctx context.Context, scope string, record runstore.CallRecord) error {
	if s.window == "before_started" && record.State == runstore.CallStarted {
		return errors.New("started write unavailable")
	}
	if err := s.Store.PutCall(ctx, scope, record); err != nil {
		return err
	}
	if s.window == "after_started_before_send" && record.State == runstore.CallStarted || s.window == "after_completion_before_return" && record.State != runstore.CallStarted {
		panic("worker crash")
	}
	return nil
}

func (s crashRunLog) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	if s.window == "before_planner_step" && event.Type == hooks.PlannerStep || s.window == "before_run_sealed" && event.Type == hooks.RunSealed {
		return runlog.AppendResult{}, errors.New("commit unavailable")
	}
	result, err := s.Store.Append(ctx, event)
	if err != nil {
		return result, err
	}
	if s.window == "after_planner_step" && event.Type == hooks.PlannerStep || s.window == "after_run_sealed" && event.Type == hooks.RunSealed {
		panic("workflow crash")
	}
	return result, nil
}

func (p *crashProvider) Complete(context.Context, *model.Request) (*model.Response, error) {
	p.sent++
	if p.window == "after_send" {
		panic("provider answered; worker crashed")
	}
	return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}}, StopReason: "end_turn"}, nil
}

func (p *crashProvider) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func (p *zombieProvider) Complete(context.Context, *model.Request) (*model.Response, error) {
	close(p.sent)
	<-p.release
	return &model.Response{StopReason: "end_turn"}, nil
}

func (p *zombieProvider) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}

// runUntilCrash simulates a worker disappearing, not graceful activity Finish.
func runUntilCrash(run func() error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			return
		}
	}()
	err := run()
	if err != nil {
		return
	}
}
