package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type identityRoundTripper func(*http.Request) (*http.Response, error)

type identityIdleTransport struct {
	identityRoundTripper
	closed atomic.Int32
}

type identityBody struct {
	io.Reader
	closed bool
}

func TestHTTPUserAgentPreservesClientAndRequestOwnership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header http.Header
		want   string
	}{
		{"default", nil, "configured/1"},
		{"existing", http.Header{"User-Agent": {"host/2"}}, "host/2"},
		{"lowercase", http.Header{"user-agent": {"host/2"}}, "host/2"},
		{"empty", http.Header{"User-Agent": {""}}, ""},
		{"suppressed", http.Header{"User-Agent": nil}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sentinel := errors.New("downstream error")
			transport := &identityIdleTransport{identityRoundTripper: func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, tc.want, req.Header.Get("User-Agent"))
				assert.Equal(t, "Bearer host-token", req.Header.Get("Authorization"))
				req.Header.Set("Authorization", "downstream-only")
				return nil, sentinel
			}}
			jar, err := cookiejar.New(nil)
			require.NoError(t, err)
			redirected := false
			host := &http.Client{Transport: transport, Jar: jar, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
				redirected = true
				return http.ErrUseLastResponse
			}}
			client, err := mcpIdentityClient(host, "configured/1")
			require.NoError(t, err)
			assert.NotSame(t, host, client)
			assert.Same(t, transport, host.Transport)
			assert.Same(t, jar, client.Jar)
			assert.Equal(t, host.Timeout, client.Timeout)
			require.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
			assert.True(t, redirected)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://unused.invalid", strings.NewReader("payload"))
			require.NoError(t, err)
			req.Header = tc.header.Clone()
			if req.Header == nil {
				req.Header = make(http.Header)
			}
			req.Header.Set("Authorization", "Bearer host-token")
			original := req.Header.Clone()
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					response, err := client.Transport.RoundTrip(req)
					if response != nil {
						if closeErr := response.Body.Close(); closeErr != nil {
							t.Errorf("close unexpected response: %v", closeErr)
						}
					}
					assert.Nil(t, response)
					assert.ErrorIs(t, err, sentinel)
				})
			}
			wg.Wait()
			assert.Equal(t, original, req.Header)
			assert.Zero(t, transport.closed.Load())
			client.CloseIdleConnections()
			assert.Equal(t, int32(1), transport.closed.Load())
		})
	}
}

func TestHTTPUserAgentPreservesBodyContextAndResponse(t *testing.T) {
	body := &identityBody{Reader: strings.NewReader("payload")}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unused.invalid", body)
	require.NoError(t, err)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("payload")), nil
	}
	responseBody := &identityBody{Reader: strings.NewReader("response")}
	expected := &http.Response{StatusCode: http.StatusOK, Body: responseBody}
	host := &http.Client{Transport: identityRoundTripper(func(sent *http.Request) (*http.Response, error) {
		assert.NotSame(t, req, sent)
		assert.Equal(t, " custom/1\t(comment) ", sent.Header.Get("User-Agent"))
		assert.Same(t, req.Body, sent.Body)
		assert.Same(t, ctx, sent.Context())
		replay, err := sent.GetBody()
		require.NoError(t, err)
		data, err := io.ReadAll(replay)
		require.NoError(t, err)
		require.NoError(t, replay.Close())
		assert.Equal(t, "payload", string(data))
		assert.False(t, body.closed)
		require.NoError(t, sent.Body.Close())
		cancel()
		assert.ErrorIs(t, sent.Context().Err(), context.Canceled)
		return expected, nil
	})}
	client, err := mcpIdentityClient(host, " custom/1\t(comment) ")
	require.NoError(t, err)
	response, err := client.Transport.RoundTrip(req)
	require.NoError(t, err)
	assert.Same(t, expected, response)
	assert.True(t, body.closed, "downstream transport owns request closure")
	assert.False(t, responseBody.closed, "response remains owned by caller")
	require.NoError(t, response.Body.Close())
	assert.True(t, responseBody.closed)
	assert.Empty(t, req.Header)
}

func TestHTTPUserAgentUsesCurrentDefaultTransport(t *testing.T) {
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	host := &http.Client{}
	client, err := mcpIdentityClient(host, "configured/1")
	require.NoError(t, err)
	sentinel := errors.New("current default")
	transport := &identityIdleTransport{identityRoundTripper: func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "configured/1", req.Header.Get("User-Agent"))
		return nil, sentinel
	}}
	http.DefaultTransport = transport
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://unused.invalid", nil)
	require.NoError(t, err)
	response, err := client.Transport.RoundTrip(req)
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	assert.Nil(t, response)
	require.ErrorIs(t, err, sentinel)
	assert.Nil(t, host.Transport)
	client.CloseIdleConnections()
	assert.Equal(t, int32(1), transport.closed.Load())
}

func TestHTTPUserAgentRejectsInvalidConfigurationBeforeIO(t *testing.T) {
	for _, value := range []string{"bad\rvalue", "bad\nvalue", "bad\x00value", "bad\x01value", "bad\x7fvalue"} {
		for _, constructor := range []func(context.Context, HTTPOptions) (*SessionCaller, error){NewHTTPCaller, NewSSECaller} {
			called := false
			client := &http.Client{Transport: identityRoundTripper(func(*http.Request) (*http.Response, error) {
				called = true
				return nil, errors.New("unexpected I/O")
			})}
			caller, err := constructor(t.Context(), HTTPOptions{Endpoint: "http://unused.invalid", Client: client, UserAgent: value})
			assert.Nil(t, caller)
			require.ErrorContains(t, err, "User-Agent")
			assert.False(t, called)
		}
	}
}

func TestHTTPUserAgentPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unused.invalid", nil)
	require.NoError(t, err)
	host := &http.Client{Transport: identityRoundTripper(func(sent *http.Request) (*http.Response, error) {
		return nil, sent.Context().Err()
	})}
	client, err := mcpIdentityClient(host, "configured/1")
	require.NoError(t, err)
	response, err := client.Transport.RoundTrip(req)
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	assert.Nil(t, response)
	require.ErrorIs(t, err, context.Canceled)
}

func (f identityRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (t *identityIdleTransport) CloseIdleConnections() {
	t.closed.Add(1)
}

func (b *identityBody) Close() error {
	b.closed = true
	return nil
}
