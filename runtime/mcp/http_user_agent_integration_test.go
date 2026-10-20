package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPUserAgentAcrossTransports(t *testing.T) {
	for _, protocol := range []string{"http", "sse"} {
		for _, tc := range []struct {
			name, configured, override, want string
			custom                           bool
		}{
			{"default", "", "", "loom-mcp/dev", false},
			{"configured", "application/2 (service)", "", "application/2 (service)", false},
			{"host override", "application/2", "host-owned/3", "host-owned/3", true},
			{"host suppression", "application/2", "", "", true},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				identity := make(chan *sdkmcp.Implementation, 1)
				server := newSDKTestServerWithOptions(&sdkmcp.ServerOptions{InitializedHandler: func(_ context.Context, req *sdkmcp.InitializedRequest) {
					identity <- req.Session.InitializeParams().ClientInfo
				}})
				var handler http.Handler
				constructor := NewHTTPCaller
				if protocol == "sse" {
					handler = sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server {
						return server
					}, nil)
					constructor = NewSSECaller
				} else {
					handler = sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
						return server
					}, &sdkmcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
				}
				var mu sync.Mutex
				var agents [][]string
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					agents = append(agents, append([]string(nil), r.Header.Values("User-Agent")...))
					mu.Unlock()
					handler.ServeHTTP(w, r)
				}))
				t.Cleanup(endpoint.Close)
				var host *http.Client
				var transport *identityIdleTransport
				if tc.custom {
					transport = &identityIdleTransport{identityRoundTripper: func(req *http.Request) (*http.Response, error) {
						assert.Equal(t, tc.configured, req.Header.Get("User-Agent"))
						req.Header.Set("User-Agent", tc.override)
						return http.DefaultTransport.RoundTrip(req)
					}}
					host = &http.Client{Transport: transport}
				}
				caller, err := constructor(t.Context(), HTTPOptions{Endpoint: endpoint.URL, Client: host, ClientName: "host-application", ClientVersion: "42", UserAgent: tc.configured})
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, caller.Close())
				})
				// Notifications have no response acknowledgment. Wait for the server's
				// initialization handler before closing the session and canceling queued work.
				assert.Equal(t, &sdkmcp.Implementation{Name: "host-application", Version: "42"}, requireReceive(t, identity))
				assertMultiContentResponse(t, caller, t.Context())
				require.NoError(t, caller.Close())
				if transport != nil {
					assert.Zero(t, transport.closed.Load(), "closing the MCP session does not close host-owned idle connections")
				}
				mu.Lock()
				defer mu.Unlock()
				require.GreaterOrEqual(t, len(agents), 3, "initialization and later tool traffic must be observed")
				for _, values := range agents {
					if tc.want == "" {
						assert.Empty(t, values)
					} else {
						assert.Equal(t, []string{tc.want}, values)
					}
				}
			})
		}
	}
}
