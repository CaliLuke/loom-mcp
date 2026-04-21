package mcp

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

var stubTLS = tls.ConnectionState{}

func TestProtectedResourceMetadataPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		mount string
		want  string
	}{
		{"empty", "", "/.well-known/oauth-protected-resource"},
		{"root slash", "/", "/.well-known/oauth-protected-resource"},
		{"subpath", "/mcp", "/.well-known/oauth-protected-resource/mcp"},
		{"subpath trailing slash", "/mcp/", "/.well-known/oauth-protected-resource/mcp"},
		{"nested", "/api/v1/mcp", "/.well-known/oauth-protected-resource/api/v1/mcp"},
		{"leading slash missing", "api/v1", "/.well-known/oauth-protected-resource/api/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ProtectedResourceMetadataPath(tc.mount))
		})
	}
}

func TestBuildBearerChallenge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		url      string
		scope    string
		expected string
	}{
		{
			"with scope",
			"https://api.example.com/.well-known/oauth-protected-resource/mcp",
			"read write",
			`Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp", scope="read write"`,
		},
		{
			"empty scope omits parameter",
			"https://api.example.com/.well-known/oauth-protected-resource",
			"",
			`Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource"`,
		},
		{
			"escaped quotes in url",
			`https://example.com/"weird"/path`,
			"read",
			`Bearer resource_metadata="https://example.com/\"weird\"/path", scope="read"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, BuildBearerChallenge(tc.url, tc.scope))
		})
	}
}

func TestCanonicalizeResourceURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		host    string
		path    string
		headers map[string]string
		tls     bool
		want    string
	}{
		{
			name: "direct http",
			host: "api.example.com",
			path: "/mcp",
			want: "http://api.example.com/mcp",
		},
		{
			name: "direct https via TLS",
			host: "api.example.com",
			path: "/mcp",
			tls:  true,
			want: "https://api.example.com/mcp",
		},
		{
			name:    "X-Forwarded-Proto promotes http to https",
			host:    "api.example.com",
			path:    "/mcp",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    "https://api.example.com/mcp",
		},
		{
			name:    "X-Forwarded-Host overrides Host",
			host:    "internal.local",
			path:    "/mcp",
			headers: map[string]string{"X-Forwarded-Host": "public.example.com", "X-Forwarded-Proto": "https"},
			want:    "https://public.example.com/mcp",
		},
		{
			name:    "Forwarded header (RFC 7239) honored",
			host:    "internal.local",
			path:    "/mcp",
			headers: map[string]string{"Forwarded": `for=1.2.3.4;host=public.example.com;proto=https`},
			want:    "https://public.example.com/mcp",
		},
		{
			name:    "chained X-Forwarded-Host picks first",
			host:    "internal.local",
			path:    "/mcp",
			headers: map[string]string{"X-Forwarded-Host": "edge.example.com, lb.internal", "X-Forwarded-Proto": "https, https"},
			want:    "https://edge.example.com/mcp",
		},
		{
			name: "host lowercased",
			host: "API.Example.COM",
			path: "/mcp",
			want: "http://api.example.com/mcp",
		},
		{
			name: "default http port stripped",
			host: "api.example.com:80",
			path: "/mcp",
			want: "http://api.example.com/mcp",
		},
		{
			name: "default https port stripped",
			host: "api.example.com:443",
			path: "/mcp",
			tls:  true,
			want: "https://api.example.com/mcp",
		},
		{
			name: "non-default port retained",
			host: "api.example.com:8443",
			path: "/mcp",
			tls:  true,
			want: "https://api.example.com:8443/mcp",
		},
		{
			name: "trailing slash stripped",
			host: "api.example.com",
			path: "/mcp/",
			want: "http://api.example.com/mcp",
		},
		{
			name: "root path preserved",
			host: "api.example.com",
			path: "/",
			want: "http://api.example.com/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+tc.host+tc.path, nil)
			r.Host = tc.host
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if tc.tls {
				r.TLS = &stubTLS
			}
			assert.Equal(t, tc.want, CanonicalizeResourceURL(r))
		})
	}
}
