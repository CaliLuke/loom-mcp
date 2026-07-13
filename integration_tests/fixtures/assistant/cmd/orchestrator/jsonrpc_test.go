package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewHTTPServerUsesBoundedStreamingSafeTimeouts(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	server := newHTTPServer("127.0.0.1:0", handler)

	assert.Equal(t, 60*time.Second, server.ReadHeaderTimeout)
	assert.Equal(t, 15*time.Second, server.ReadTimeout)
	assert.Zero(t, server.WriteTimeout)
	assert.Equal(t, 60*time.Second, server.IdleTimeout)
}
