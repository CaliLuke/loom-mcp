package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type credentialEvaluationBody struct {
	io.ReadCloser
	once     sync.Once
	closeErr error
	closed   chan struct{}
}

func TestCredentialRefreshHostBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                                                string
		status, repeats, tokens, resources                  int
		expired, refreshFail, nonReplayable, effect, absent bool
	}{
		{name: "preflight renewal succeeds", status: 200, repeats: 1, expired: true, tokens: 1, resources: 1},
		{name: "preflight renewal fails", status: 200, repeats: 1, expired: true, refreshFail: true, tokens: 1},
		{name: "repeated rejection", status: 401, repeats: 2, resources: 2},
		{name: "permission denied", status: 403, repeats: 1, resources: 1},
		{name: "non-replayable rejection", status: 401, repeats: 1, nonReplayable: true, resources: 1},
		{name: "effect before rejection", status: 401, repeats: 1, effect: true, resources: 1},
		{name: "feature absent", status: 401, repeats: 1, absent: true, resources: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tokens, resources, effects atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokens.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if tc.refreshFail {
						w.WriteHeader(http.StatusBadRequest)
						_, err := io.WriteString(w, `{"error":"invalid_grant"}`)
						assert.NoError(t, err)
						return
					}
					_, err := io.WriteString(w, `{"access_token":"renewed-fixture","token_type":"Bearer","expires_in":3600}`)
					assert.NoError(t, err)
					return
				}
				resources.Add(1)
				if tc.effect {
					effects.Add(1)
				}
				wantAuth := "Bearer existing-fixture"
				if tc.expired {
					wantAuth = "Bearer renewed-fixture"
				}
				if tc.absent {
					wantAuth = ""
				}
				assert.Equal(t, wantAuth, r.Header.Get("Authorization"))
				assert.Equal(t, "host-value", r.Header.Get("X-Host"))
				data, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, "mutation payload", string(data))
				w.WriteHeader(tc.status)
				_, err = io.WriteString(w, "resource-response")
				assert.NoError(t, err)
			}))
			t.Cleanup(server.Close)
			client := server.Client()
			if !tc.absent {
				client = credentialEvaluationClient(t.Context(), server, tc.expired)
			}
			wrapped, err := mcpIdentityClient(client, "probe/1")
			require.NoError(t, err)
			for range tc.repeats {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/resource", strings.NewReader("mutation payload"))
				require.NoError(t, err)
				body := &credentialEvaluationBody{ReadCloser: req.Body, closed: make(chan struct{})}
				req.Body = body
				var recreations atomic.Int32
				recreate := req.GetBody
				req.GetBody = func() (io.ReadCloser, error) {
					recreations.Add(1)
					return recreate()
				}
				if tc.nonReplayable {
					req.GetBody = nil
				}
				req.Header.Set("X-Host", "host-value")
				original := req.Header.Clone()
				response, err := wrapped.Do(req)
				if tc.refreshFail {
					require.Error(t, err)
					var retrieve *oauth2.RetrieveError
					require.ErrorAs(t, err, &retrieve)
					assert.Equal(t, "invalid_grant", retrieve.ErrorCode)
					assert.Nil(t, response)
				} else {
					require.NoError(t, err)
					assert.Equal(t, tc.status, response.StatusCode)
					data, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					assert.Equal(t, "resource-response", string(data))
				}
				requireReceive(t, body.closed)
				assert.Equal(t, original, req.Header)
				assert.Same(t, body, req.Body)
				assert.Zero(t, recreations.Load())
			}
			assert.EqualValues(t, tc.tokens, tokens.Load())
			assert.EqualValues(t, tc.resources, resources.Load())
			if tc.effect {
				assert.Equal(t, int32(1), effects.Load())
			}
		})
	}
}

func TestCredentialRefreshConcurrent(t *testing.T) {
	const callers = 8
	var tokens, resources atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if tokens.Add(1) == 1 {
				close(started)
			}
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, err := io.WriteString(w, `{"access_token":"renewed-fixture","token_type":"Bearer","expires_in":3600}`)
			assert.NoError(t, err)
			return
		}
		resources.Add(1)
		assert.Equal(t, "Bearer renewed-fixture", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	client := credentialEvaluationClient(t.Context(), server, true)
	results := make(chan error, callers)
	for range callers {
		go func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/resource", strings.NewReader("payload"))
			if err != nil {
				results <- err
				return
			}
			response, err := client.Do(req)
			if response != nil {
				err = errors.Join(err, response.Body.Close())
			}
			results <- err
		}()
	}
	requireReceive(t, started)
	unblock()
	for range callers {
		require.NoError(t, requireReceive(t, results))
	}
	assert.Equal(t, int32(1), tokens.Load())
	assert.Equal(t, int32(callers), resources.Load())
}

func TestCredentialRefreshCancellation(t *testing.T) {
	for _, separate := range []bool{false, true} {
		name := "host refresh context"
		if separate {
			name = "request context cannot cancel host refresh"
		}
		t.Run(name, func(t *testing.T) {
			started, inspect, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			inspected := make(chan error, 1)
			var releaseOnce sync.Once
			unblock := func() {
				releaseOnce.Do(func() {
					close(release)
				})
			}
			var tokens, resources atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokens.Add(1)
					_, err := io.Copy(io.Discard, r.Body)
					assert.NoError(t, err)
					close(started)
					select {
					case <-inspect:
						inspected <- r.Context().Err()
					case <-release:
						return
					}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, err = io.WriteString(w, `{"access_token":"renewed-fixture","token_type":"Bearer","expires_in":3600}`)
					assert.NoError(t, err)
					return
				}
				resources.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			t.Cleanup(unblock)
			refreshCtx, cancelRefresh := context.WithCancel(t.Context())
			defer cancelRefresh()
			requestCtx, cancelRequest := context.WithCancel(refreshCtx)
			defer cancelRequest()
			client := credentialEvaluationClient(refreshCtx, server, true)
			result := make(chan error, 1)
			go func() {
				req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, server.URL+"/resource", strings.NewReader("payload"))
				if err != nil {
					result <- err
					return
				}
				response, err := client.Do(req)
				if response != nil {
					err = errors.Join(err, response.Body.Close())
				}
				result <- err
			}()
			requireReceive(t, started)
			if separate {
				cancelRequest()
				close(inspect)
				require.NoError(t, requireReceive(t, inspected), "token source uses its host context")
				unblock()
			} else {
				cancelRefresh()
			}
			require.ErrorIs(t, requireReceive(t, result), context.Canceled)
			assert.Equal(t, int32(1), tokens.Load())
			assert.Zero(t, resources.Load())
		})
	}
}

// credentialEvaluationClient uses the existing OAuth cache and host-scoped refresh context.
func credentialEvaluationClient(ctx context.Context, server *httptest.Server, expired bool) *http.Client {
	token := &oauth2.Token{AccessToken: "existing-fixture", RefreshToken: "refresh-fixture", Expiry: time.Now().Add(time.Hour)}
	if expired {
		token.Expiry = time.Unix(1, 0)
	}
	cfg := &oauth2.Config{ClientID: "fixture", Endpoint: oauth2.Endpoint{TokenURL: server.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	return &http.Client{Transport: &oauth2.Transport{Source: cfg.TokenSource(ctx, token), Base: server.Client().Transport}}
}

func (b *credentialEvaluationBody) Close() error {
	b.once.Do(func() {
		b.closeErr = b.ReadCloser.Close()
		close(b.closed)
	})
	return b.closeErr
}
