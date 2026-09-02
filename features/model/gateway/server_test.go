package gateway

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type stubStreamer struct{ meta map[string]any }

func (s *stubStreamer) Recv() (model.Chunk, error) { return nil, io.EOF }
func (s *stubStreamer) Close() error               { return nil }
func (s *stubStreamer) Metadata() map[string]any   { return s.meta }

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
		return func(ctx context.Context, req *model.Request, send func(model.Chunk) error) error {
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
	if err := srv.Stream(context.Background(), &model.Request{Model: "m"}, func(model.Chunk) error { return nil }); err != nil {
		t.Fatalf("Stream error: %v", err)
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

	err = srv.Stream(context.Background(), &model.Request{Model: "m"}, func(model.Chunk) error {
		return sendErr
	})
	if !errors.Is(err, sendErr) {
		t.Fatalf("Stream error = %v, want %v", err, sendErr)
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
}

func (s *singleChunkStreamer) Recv() (model.Chunk, error) {
	if s.sent {
		return nil, io.EOF
	}
	s.sent = true
	return model.TextChunk{}, nil
}

func (s *singleChunkStreamer) Close() error             { return nil }
func (s *singleChunkStreamer) Metadata() map[string]any { return nil }
