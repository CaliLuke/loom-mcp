package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingTestStore struct {
	runstore.Store
	writes         []string
	failStarted    bool
	failCompletion bool
	failResponse   bool
}

type recordingTestClient struct {
	store    *recordingTestStore
	response *model.Response
	err      error
	stream   *recordingTestStream
	sent     int
}

type recordingTestStream struct {
	chunks    []model.Chunk
	response  *model.Response
	terminal  error
	cleanup   error
	finalizes int
	closes    int
	eof       bool
}

func TestCallRecordingUnary(t *testing.T) {
	failure := errors.New("provider failure")
	for _, test := range []struct {
		name                          string
		provider                      error
		started, completion, response bool
	}{
		{name: "success"}, {name: "provider", provider: failure}, {name: "started_write", started: true}, {name: "completion_write", completion: true}, {name: "response_write", response: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &recordingTestStore{Store: inmem.New(), failStarted: test.started, failCompletion: test.completion, failResponse: test.response}
			recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 7)
			client := &recordingTestClient{store: store, response: &model.Response{StopReason: "end", Usage: model.TokenUsage{InputTokens: 2, TotalTokens: 2}}, err: test.provider}
			wrapper := &callRecordingClient{inner: client, recording: recording}
			ctx := recordingCallContext(1)
			response, err := wrapper.Complete(ctx, &model.Request{Model: "provider", ModelClass: "class"})
			if test.provider != nil || test.started || test.completion || test.response {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Same(t, client.response, response)
			}
			if test.provider != nil {
				require.ErrorIs(t, err, failure)
			}
			calls, listErr := store.ListCalls(ctx, "root", "run")
			require.NoError(t, listErr)
			if test.started {
				assert.Zero(t, client.sent)
				assert.Empty(t, calls)
				return
			}
			assert.Equal(t, 1, client.sent)
			require.Len(t, calls, 1)
			started, decodeErr := callrecord.DecodeStarted(calls[0].Started)
			require.NoError(t, decodeErr)
			assert.Equal(t, 7, started.Step)
			assert.Equal(t, "turn", started.TurnID)
			assert.Equal(t, "registered", started.ModelID)
			assert.Equal(t, callrecord.ActivityPlanner, started.Activity)
			if test.completion || test.response {
				assert.Equal(t, runstore.CallStarted, calls[0].State)
				assert.Empty(t, calls[0].Completion)
			} else {
				completion, decodeErr := callrecord.DecodeCompletion(calls[0].Completion)
				require.NoError(t, decodeErr)
				assert.NotNil(t, completion.Response)
				outcome := contextmeasure.OutcomeOK
				if test.provider != nil {
					outcome = contextmeasure.OutcomeError
				}
				assert.Equal(t, &contextmeasure.RequestFacts{Call: calls[0].Key, Model: "provider", InputTokens: 2, Outcome: outcome}, completion.Facts)
				if test.provider != nil {
					assert.Equal(t, runstore.CallFailed, calls[0].State)
					require.NotNil(t, completion.Error)
					assert.Equal(t, failure.Error(), completion.Error.Message)
				} else {
					assert.Equal(t, runstore.CallCompleted, calls[0].State)
					assert.Nil(t, completion.Error)
				}
				assert.Equal(t, []string{"started", "send", "response", "completed"}, store.writes)
			}
		})
	}
}

func TestCallRecordingRequiresIdentity(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), recordingCallContext(0)} {
		store := &recordingTestStore{Store: inmem.New()}
		client := &recordingTestClient{store: store}
		wrapper := &callRecordingClient{inner: client, recording: NewCallRecording(store, "root", index.ViewRef{}, nil, 0)}
		_, err := wrapper.Complete(ctx, &model.Request{})
		require.Error(t, err)
		_, err = wrapper.Stream(ctx, &model.Request{})
		require.Error(t, err)
		assert.Zero(t, client.sent)
	}
}

func TestCallRecordingStream(t *testing.T) {
	failure := errors.New("stream failure")
	for _, mode := range []string{"finalize", "close", "recv_error", "cleanup_error", "abandoned", "setup_error", "wrapped_eof"} {
		t.Run(mode, func(t *testing.T) {
			store := &recordingTestStore{Store: inmem.New()}
			recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
			inner := &recordingTestStream{chunks: []model.Chunk{model.UsageChunk{Usage: model.TokenUsage{InputTokens: 2, TotalTokens: 2, Model: "provider"}}, model.UsageChunk{Usage: model.TokenUsage{OutputTokens: 3, TotalTokens: 3}}}, response: &model.Response{StopReason: "end"}, terminal: io.EOF}
			client := &recordingTestClient{store: store, stream: inner}
			if mode == "setup_error" {
				client.err = failure
			}
			wrapper := &callRecordingClient{inner: client, recording: recording}
			ctx := recordingCallContext(1)
			stream, err := wrapper.Stream(ctx, &model.Request{Stream: true})
			if mode == "setup_error" {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
				if mode == "recv_error" {
					inner.terminal = failure
				}
				if mode == "wrapped_eof" {
					inner.terminal = fmt.Errorf("wrapped terminal: %w", io.EOF)
				}
				if mode == "cleanup_error" {
					inner.cleanup = failure
				}
				if mode != "abandoned" {
					for range 2 {
						_, err = stream.Recv()
						require.NoError(t, err)
					}
					_, err = stream.Recv()
					if mode == "close" {
						err = stream.Close()
						require.NoError(t, err)
						require.NoError(t, stream.Close())
					} else {
						if errors.Is(err, io.EOF) {
							err = nil
						}
						err = stream.Finalize(err)
						switch mode {
						case "recv_error", "cleanup_error":
							require.ErrorIs(t, err, failure)
						default:
							require.NoError(t, err)
						}
						assert.Equal(t, err, stream.Finalize(nil))
					}
				}
			}
			require.NoError(t, recording.Finish(ctx))
			require.NoError(t, recording.Finish(ctx))
			calls, err := store.ListCalls(ctx, "root", "run")
			require.NoError(t, err)
			require.Len(t, calls, 1)
			completion, err := callrecord.DecodeCompletion(calls[0].Completion)
			require.NoError(t, err)
			outcome, input := contextmeasure.OutcomeError, 0
			if mode == "finalize" || mode == "close" || mode == "wrapped_eof" {
				outcome = contextmeasure.OutcomeOK
			}
			if mode != "abandoned" && mode != "setup_error" {
				input = 2
			}
			assert.Equal(t, &contextmeasure.RequestFacts{Call: calls[0].Key, Model: "registered", InputTokens: input, Outcome: outcome}, completion.Facts)
			if mode == "finalize" || mode == "close" || mode == "wrapped_eof" {
				assert.Equal(t, runstore.CallCompleted, calls[0].State)
				assert.NotNil(t, completion.Response)
				assert.Nil(t, completion.Error)
			} else {
				assert.Equal(t, runstore.CallFailed, calls[0].State)
				require.NotNil(t, completion.Error)
			}
			if mode == "abandoned" {
				assert.Equal(t, "stream_abandoned", completion.Error.Kind)
			} else if mode != "setup_error" {
				assert.Equal(t, 2, completion.Usage.InputTokens)
				assert.Equal(t, 3, completion.Usage.OutputTokens)
				assert.Equal(t, 5, completion.Usage.TotalTokens)
				assert.Equal(t, "provider", completion.Usage.Model)
			}
			assert.Len(t, recording.Keys(), 1)
		})
	}
}

func TestCallRecordingFirstGrowth(t *testing.T) {
	store := &recordingTestStore{Store: inmem.New()}
	recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
	wrapper := &callRecordingClient{inner: &recordingTestClient{store: store, response: &model.Response{}}, recording: recording}
	for seq := 1; seq <= 3; seq++ {
		ctx := model.WithCallInfo(recordingCallContext(1), model.CallInfo{RunID: "run", RootRunID: "root", Attempt: 1, Seq: seq})
		_, err := wrapper.Complete(ctx, &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "own growing history"}}}}})
		require.NoError(t, err)
	}
	growth := recording.RequestGrowth()
	require.NotNil(t, growth)
	assert.Equal(t, 1, growth.Key.Seq)
	assert.Positive(t, growth.Outside)
	assert.Zero(t, growth.Inside)
	growth.Key.Seq = 99
	assert.Equal(t, 1, recording.RequestGrowth().Key.Seq)
}

func TestCallRecordingActivityAbandonsStreams(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, fails := range []bool{false, true} {
			failure := errors.New("planner failure")
			rt, _, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
				client, ok := agent.ModelClient("registered-model")
				require.True(t, ok)
				_, err := client.Stream(ctx, &model.Request{})
				if err != nil {
					return err
				}
				if fails {
					return failure
				}
				return nil
			})
			ctx := engine.WithActivityAttempt(context.Background(), 1)
			var err error
			if resume {
				_, err = rt.PlanResumeActivity(ctx, input)
			} else {
				_, err = rt.PlanStartActivity(ctx, input)
			}
			if fails {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorContains(t, err, "was not finalized")
			}
			calls, err := rt.RunStore.ListCalls(ctx, "root-run", "child-run")
			require.NoError(t, err)
			require.Len(t, calls, 1)
			assert.Equal(t, runstore.CallFailed, calls[0].State)
			completion, err := callrecord.DecodeCompletion(calls[0].Completion)
			require.NoError(t, err)
			require.NotNil(t, completion.Error)
			assert.Equal(t, "stream_abandoned", completion.Error.Kind)
		}
	}
}

func TestCallRecordingFinishReportsCaughtWriteFailure(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		store := &recordingTestStore{Store: inmem.New(), failCompletion: true}
		recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
		inner := &recordingTestStream{terminal: io.EOF, response: &model.Response{}}
		client := &callRecordingClient{inner: &recordingTestClient{store: store, stream: inner, response: &model.Response{}}, recording: recording}
		ctx := recordingCallContext(1)
		if streaming {
			stream, err := client.Stream(ctx, &model.Request{})
			require.NoError(t, err)
			_, err = stream.Recv()
			require.ErrorIs(t, err, io.EOF)
			require.ErrorContains(t, stream.Finalize(nil), "completion store failure")
		} else {
			_, err := client.Complete(ctx, &model.Request{})
			require.ErrorContains(t, err, "completion store failure")
		}
		require.ErrorContains(t, recording.Finish(ctx), "completion store failure")
	}
}

func TestCallRecordingStreamConcurrentFinalization(t *testing.T) {
	store := &recordingTestStore{Store: inmem.New()}
	recording := NewCallRecording(store, "root", index.ViewRef{}, nil, 0)
	inner := &recordingTestStream{terminal: io.EOF, response: &model.Response{}}
	client := &callRecordingClient{inner: &recordingTestClient{store: store, stream: inner}, recording: recording}
	ctx := recordingCallContext(1)
	stream, err := client.Stream(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	var wg sync.WaitGroup
	outcomes := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			outcomes <- stream.Finalize(nil)
		})
	}
	wg.Wait()
	close(outcomes)
	for err := range outcomes {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, inner.finalizes)
	calls, err := store.ListCalls(ctx, "root", "run")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, runstore.CallCompleted, calls[0].State)
	completion, err := callrecord.DecodeCompletion(calls[0].Completion)
	require.NoError(t, err)
	require.NotNil(t, completion.Facts)
	assert.Equal(t, calls[0].Key, completion.Facts.Call)
	assert.Equal(t, contextmeasure.OutcomeOK, completion.Facts.Outcome)
}

func (s *recordingTestStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if len(data) > 0 && bytes.Contains(data, []byte(`"StopReason"`)) {
		s.writes = append(s.writes, "response")
		if s.failResponse {
			return "", errors.New("response store failure")
		}
	}
	return s.Store.Put(ctx, scope, data)
}

func (s *recordingTestStore) PutCall(ctx context.Context, scope string, record runstore.CallRecord) error {
	if record.State == runstore.CallStarted {
		s.writes = append(s.writes, "started")
		if s.failStarted {
			return errors.New("started store failure")
		}
	} else {
		s.writes = append(s.writes, "completed")
		if s.failCompletion {
			return errors.New("completion store failure")
		}
	}
	return s.Store.PutCall(ctx, scope, record)
}

func (c *recordingTestClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	c.sent++
	c.store.writes = append(c.store.writes, "send")
	return c.response, c.err
}

func (c *recordingTestClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	c.sent++
	c.store.writes = append(c.store.writes, "send")
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}

func (s *recordingTestStream) Recv() (model.Chunk, error) {
	if len(s.chunks) > 0 {
		chunk := s.chunks[0]
		s.chunks = s.chunks[1:]
		return chunk, nil
	}
	s.eof = errors.Is(s.terminal, io.EOF)
	return nil, s.terminal
}

func (s *recordingTestStream) Response() *model.Response {
	if !s.eof {
		return nil
	}
	return s.response
}

func (s *recordingTestStream) Finalize(primary error) error {
	s.finalizes++
	return errors.Join(primary, s.cleanup)
}

func (s *recordingTestStream) Close() error {
	s.closes++
	return s.cleanup
}

func recordingCallContext(attempt int) context.Context {
	return model.WithCallInfo(context.Background(), model.CallInfo{RunID: "run", RootRunID: "root", Seq: 1, Attempt: attempt, ModelID: "registered", TurnID: "turn"})
}
