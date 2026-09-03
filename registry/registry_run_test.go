package registry

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunGRPCServerClosesRegistryAfterServeFailure(t *testing.T) {
	serveErr := errors.New("listener failed")
	closeErr := errors.New("health tracker failed to close")
	server := &failingGRPCServer{err: serveErr}
	tracker := &closingHealthTracker{err: closeErr}
	reg := &Registry{healthTracker: tracker}

	err := reg.runGRPCServer(context.Background(), server, nil)

	assert.False(t, server.gracefullyStopped)
	assert.True(t, tracker.closed)
	require.ErrorIs(t, err, serveErr)
	require.ErrorContains(t, err, closeErr.Error())
}

func TestRunGRPCServerPreservesServeFailureDuringShutdown(t *testing.T) {
	serveErr := errors.New("listener failed during shutdown")
	server := newShutdownRaceGRPCServer(serveErr)
	tracker := &closingHealthTracker{}
	reg := &Registry{healthTracker: tracker}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := reg.runGRPCServer(ctx, server, nil)

	assert.True(t, server.gracefullyStopped)
	assert.True(t, tracker.closed)
	require.ErrorIs(t, err, serveErr)
}

type failingGRPCServer struct {
	err               error
	gracefullyStopped bool
}

func (s *failingGRPCServer) Serve(net.Listener) error {
	return s.err
}

func (s *failingGRPCServer) GracefulStop() {
	s.gracefullyStopped = true
}

type shutdownRaceGRPCServer struct {
	err               error
	shutdownCh        chan struct{}
	shutdownOnce      sync.Once
	gracefullyStopped bool
}

func newShutdownRaceGRPCServer(err error) *shutdownRaceGRPCServer {
	return &shutdownRaceGRPCServer{err: err, shutdownCh: make(chan struct{})}
}

func (s *shutdownRaceGRPCServer) Serve(net.Listener) error {
	<-s.shutdownCh
	return s.err
}

func (s *shutdownRaceGRPCServer) GracefulStop() {
	s.gracefullyStopped = true
	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
	})
}

type closingHealthTracker struct {
	closed bool
	err    error
}

func (t *closingHealthTracker) Health(context.Context, string, string) (ToolsetHealth, error) {
	return ToolsetHealth{}, nil
}

func (t *closingHealthTracker) RecordPong(context.Context, string, string, string, string) error {
	return nil
}

func (t *closingHealthTracker) EnsurePingLoop(context.Context, string) error {
	return nil
}

func (t *closingHealthTracker) Close() error {
	t.closed = true
	return t.err
}
