package gateway

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type stubStreamer struct{ eof bool }

func (s *stubStreamer) Recv() (model.Chunk, error) {
	s.eof = true
	return nil, io.EOF
}

func (s *stubStreamer) Close() error {
	return nil
}
func (s *stubStreamer) Response() *model.Response {
	if !s.eof {
		return nil
	}
	return &model.Response{}
}

type stubProvider struct{}

func (stubProvider) Complete(_ context.Context, req *model.Request) (*model.Response, error) {
	return &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}}, nil
}
func (stubProvider) Stream(_ context.Context, _ *model.Request) (model.Streamer, error) {
	return &stubStreamer{}, nil
}

func TestNewServer_BuildsChains(t *testing.T) {
	prov := stubProvider{}
	calledUnary := false
	calledStream := false

	u := func(next UnaryHandler) UnaryHandler {
		return func(ctx context.Context, req *model.Request) (*model.Response, error) {
			calledUnary = true
			return next(ctx, req)
		}
	}
	s := func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *model.Request, send func(model.Chunk) error) (*model.Response, error) {
			calledStream = true
			return next(ctx, req, send)
		}
	}

	srv, err := NewServer(WithProvider(prov), WithUnary(u), WithStream(s))
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}

	if _, err := srv.Complete(context.Background(), &model.Request{Model: "m"}); err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	response, err := srv.Stream(context.Background(), &model.Request{Model: "m"}, func(model.Chunk) error { return nil })
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if response == nil {
		t.Fatal("Stream returned no terminal response")
	}

	if !calledUnary {
		t.Fatal("unary middleware not invoked")
	}
	if !calledStream {
		t.Fatal("stream middleware not invoked")
	}
}

func TestServerStreamReturnsSendError(t *testing.T) {
	sendErr := errors.New("send failed")
	srv, err := NewServer(WithProvider(chunkProvider{}))
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}

	_, err = srv.Stream(context.Background(), &model.Request{Model: "m"}, func(model.Chunk) error {
		return sendErr
	})
	if !errors.Is(err, sendErr) {
		t.Fatalf("Stream error = %v, want %v", err, sendErr)
	}
}

func TestServerStreamClosesStreamReturnedWithSetupError(t *testing.T) {
	setupErr := errors.New("setup failed")
	closeErr := errors.New("close failed")
	stream := &setupErrorStreamer{closeErr: closeErr}
	srv, err := NewServer(WithProvider(setupErrorProvider{stream: stream, err: setupErr}))
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}

	response, err := srv.Stream(context.Background(), &model.Request{}, func(model.Chunk) error { return nil })
	if response != nil {
		t.Fatalf("Stream response = %#v, want nil", response)
	}
	if !errors.Is(err, setupErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Stream error = %v, want setup and close errors", err)
	}
	if stream.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", stream.closeCalls)
	}
}

func TestServerStreamDoesNotCloseTypedNilStreamReturnedWithSetupError(t *testing.T) {
	setupErr := errors.New("setup failed")
	var stream *setupErrorStreamer
	srv, err := NewServer(WithProvider(setupErrorProvider{stream: stream, err: setupErr}))
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}

	response, err := srv.Stream(context.Background(), &model.Request{}, func(model.Chunk) error { return nil })
	if response != nil {
		t.Fatalf("Stream response = %#v, want nil", response)
	}
	if !errors.Is(err, setupErr) {
		t.Fatalf("Stream error = %v, want %v", err, setupErr)
	}
}

func TestServerStreamRejectsNilStreamWithoutSetupError(t *testing.T) {
	var stream *setupErrorStreamer
	srv, err := NewServer(WithProvider(setupErrorProvider{stream: stream}))
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}

	response, err := srv.Stream(context.Background(), &model.Request{}, func(model.Chunk) error { return nil })
	if response != nil {
		t.Fatalf("Stream response = %#v, want nil", response)
	}
	if err == nil || err.Error() != "gateway: provider returned a nil stream" {
		t.Fatalf("Stream error = %v, want nil-stream error", err)
	}
}

func TestServerStreamRejectsCancellationAtEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &cancelEOFStreamer{cancel: cancel}
	srv, err := NewServer(WithProvider(setupErrorProvider{stream: stream}))
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}

	response, err := srv.Stream(ctx, &model.Request{}, func(model.Chunk) error { return nil })
	if response != nil {
		t.Fatalf("Stream response = %#v, want nil", response)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream error = %v, want cancellation", err)
	}
	if stream.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", stream.closeCalls)
	}
}

type chunkProvider struct{}

func (chunkProvider) Complete(context.Context, *model.Request) (*model.Response, error) {
	return nil, nil
}

func (chunkProvider) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return &singleChunkStreamer{}, nil
}

type singleChunkStreamer struct {
	sent bool
	eof  bool
}

func (s *singleChunkStreamer) Recv() (model.Chunk, error) {
	if s.sent {
		s.eof = true
		return nil, io.EOF
	}
	s.sent = true
	return model.TextChunk{}, nil
}

func (s *singleChunkStreamer) Close() error { return nil }
func (s *singleChunkStreamer) Response() *model.Response {
	if !s.eof {
		return nil
	}
	return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant}}}
}

type setupErrorProvider struct {
	stream model.Streamer
	err    error
}

func (setupErrorProvider) Complete(context.Context, *model.Request) (*model.Response, error) {
	return nil, errors.New("unexpected complete call")
}

func (p setupErrorProvider) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return p.stream, p.err
}

type setupErrorStreamer struct {
	closeErr   error
	closeCalls int
}

func (*setupErrorStreamer) Recv() (model.Chunk, error) {
	return nil, io.EOF
}

func (s *setupErrorStreamer) Close() error {
	s.closeCalls++
	return s.closeErr
}

func (*setupErrorStreamer) Response() *model.Response {
	return nil
}

type cancelEOFStreamer struct {
	cancel     context.CancelFunc
	closeCalls int
}

func (s *cancelEOFStreamer) Recv() (model.Chunk, error) {
	s.cancel()
	return nil, io.EOF
}

func (s *cancelEOFStreamer) Close() error {
	s.closeCalls++
	return nil
}

func (*cancelEOFStreamer) Response() *model.Response {
	return &model.Response{StopReason: "end_turn"}
}
