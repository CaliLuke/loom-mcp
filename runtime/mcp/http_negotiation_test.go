package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamableHTTPNegotiation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		accept         string
		jsonResponse   bool
		wantStatus     int
		wantForwarded  bool
		wantSDKAccept  string
		wantVaryAccept bool
	}{
		{
			name:          "official client accepts both response types",
			method:        http.MethodPost,
			accept:        "application/json, text/event-stream",
			wantStatus:    http.StatusNoContent,
			wantForwarded: true,
			wantSDKAccept: "application/json, text/event-stream",
		},
		{
			name:           "JSON-only client cannot accept default SSE response",
			method:         http.MethodPost,
			accept:         "application/json",
			wantStatus:     http.StatusNotAcceptable,
			wantVaryAccept: true,
		},
		{
			name:          "JSON-only client accepts configured JSON response",
			method:        http.MethodPost,
			accept:        "application/json",
			jsonResponse:  true,
			wantStatus:    http.StatusNoContent,
			wantForwarded: true,
			wantSDKAccept: "application/json, text/event-stream",
		},
		{
			name:           "zero-quality SSE does not accept default response",
			method:         http.MethodPost,
			accept:         "application/json, text/event-stream;q=0, */*;q=0.5",
			wantStatus:     http.StatusNotAcceptable,
			wantVaryAccept: true,
		},
		{
			name:           "parameterized SSE does not override an exact rejection",
			method:         http.MethodPost,
			accept:         "text/event-stream;q=0, text/event-stream;profile=foo;q=1",
			wantStatus:     http.StatusNotAcceptable,
			wantVaryAccept: true,
		},
		{
			name:          "accept extension does not constrain the SSE representation",
			method:        http.MethodPost,
			accept:        `text/event-stream;q=1;note="a,b"`,
			wantStatus:    http.StatusNoContent,
			wantForwarded: true,
			wantSDKAccept: "application/json, text/event-stream",
		},
		{
			name:          "wildcard accepts both response types",
			method:        http.MethodPost,
			accept:        "*/*",
			wantStatus:    http.StatusNoContent,
			wantForwarded: true,
			wantSDKAccept: "*/*",
		},
		{
			name:          "GET bypasses POST negotiation",
			method:        http.MethodGet,
			wantStatus:    http.StatusNoContent,
			wantForwarded: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var forwarded *http.Request
			handler := StreamableHTTPNegotiation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded = r
				w.WriteHeader(http.StatusNoContent)
			}), tt.jsonResponse)
			req := httptest.NewRequestWithContext(t.Context(), tt.method, "/rpc", nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantVaryAccept, rec.Header().Get("Vary") == "Accept")
			if !tt.wantForwarded {
				assert.Nil(t, forwarded)
				return
			}
			require.NotNil(t, forwarded)
			assert.Equal(t, tt.wantSDKAccept, forwarded.Header.Get("Accept"))
			assert.Equal(t, tt.accept, req.Header.Get("Accept"), "middleware must not modify the application request")
		})
	}
}
