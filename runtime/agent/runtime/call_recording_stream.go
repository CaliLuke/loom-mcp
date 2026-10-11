package runtime

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type callRecordingStream struct {
	inner   model.ValidatedStreamer
	call    *recordedCall
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	usage   model.TokenUsage
	primary error
	done    bool
	result  error
}

func (s *callRecordingStream) Recv() (model.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		if s.result != nil {
			return nil, s.result
		}
		return nil, io.EOF
	}
	chunk, err := s.inner.Recv()
	if delta, ok := chunk.(model.UsageChunk); ok && err == nil {
		usage, usageErr := checkedAddTokenUsage(s.usage, delta.Usage)
		if usageErr != nil {
			s.primary = errors.Join(s.primary, usageErr)
			return nil, usageErr
		}
		usage.Model = firstNonEmpty(delta.Usage.Model, s.usage.Model)
		usage.ModelClass = delta.Usage.ModelClass
		if usage.ModelClass == "" {
			usage.ModelClass = s.usage.ModelClass
		}
		s.usage = usage
	}
	// EOF, including wrapped EOF, ends the stream without a receive failure.
	if err != nil && !errors.Is(err, io.EOF) {
		s.primary = errors.Join(s.primary, err)
	}
	return chunk, err
}

func (s *callRecordingStream) Response() *model.Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Response()
}

func (s *callRecordingStream) Finalize(primary error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return s.result
	}
	primary = errors.Join(s.primary, primary)
	response := s.inner.Response()
	terminal := errors.Join(primary, s.inner.Finalize(primary))
	if response == nil {
		response = s.inner.Response()
	}
	s.result = errors.Join(terminal, s.call.complete(s.ctx, response, s.usage, terminal, ""))
	s.done = true
	s.cancel()
	return s.result
}

func (s *callRecordingStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return s.result
	}
	response := s.inner.Response()
	terminal := errors.Join(s.primary, s.inner.Close())
	if response == nil {
		response = s.inner.Response()
	}
	if response == nil && terminal == nil {
		terminal = errors.New("model stream closed before canonical response")
	}
	s.result = errors.Join(terminal, s.call.complete(s.ctx, response, s.usage, terminal, ""))
	s.done = true
	s.cancel()
	return s.result
}

// abandon shares the terminal lock with Finalize and Close, so one path writes.
func (s *callRecordingStream) abandon(ctx context.Context) error {
	// Cancel provider reads before waiting for the terminal lock.
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	primary := errors.New("model stream still open at activity end")
	response := s.inner.Response()
	terminal := errors.Join(primary, s.primary, s.inner.Finalize(primary))
	if response == nil {
		response = s.inner.Response()
	}
	persistence := s.call.complete(context.WithoutCancel(ctx), response, s.usage, terminal, "stream_abandoned")
	s.result = errors.Join(terminal, persistence)
	s.done = true
	return persistence
}
