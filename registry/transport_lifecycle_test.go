package registry_test

import (
	"net"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type registryStartupListener struct {
	net.Listener
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func TestRegistryTransportServerWaitsForStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := &registryStartupListener{
			Listener: bufconn.Listen(1024),
			entered:  make(chan struct{}),
			release:  make(chan struct{}),
		}
		server := grpc.NewServer()
		started := make(chan (<-chan error), 1)
		go func() {
			started <- startRegistryTransportServer(t, server, listener)
		}()
		<-listener.entered
		synctest.Wait()
		assert.Empty(t, started, "setup must wait until Serve registers its listener")

		close(listener.release)
		done := <-started
		server.Stop()
		assert.NoError(t, <-done)
	})
}

// Addr holds Serve before listener registration, making the startup race reproducible.
func (listener *registryStartupListener) Addr() net.Addr {
	listener.once.Do(func() {
		close(listener.entered)
		<-listener.release
	})
	return listener.Listener.Addr()
}
