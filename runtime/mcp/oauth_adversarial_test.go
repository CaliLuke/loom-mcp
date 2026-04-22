package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Adversarial coverage for the RFC 9728 / RFC 6750 / RFC 7239 plumbing.
// Each subtest documents a concrete attack or misuse and asserts that
// the implementation fails loud (strict path) or degrades safely
// (lenient challenge path), never silently substituting attacker input
// into an identity claim.

const advTestAPIHost = "api.example.com"

func TestCanonicalizeResourceURLStrict_RejectsMalformedForwardedHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
		value  string
	}{
		{"X-Forwarded-Host with path separator", "X-Forwarded-Host", "evil.com/path"},
		{"X-Forwarded-Host with fragment", "X-Forwarded-Host", "evil.com#frag"},
		{"X-Forwarded-Host with query", "X-Forwarded-Host", "evil.com?x=y"},
		{"X-Forwarded-Host with userinfo", "X-Forwarded-Host", "user:pass@evil.com"},
		{"X-Forwarded-Host with CR injection", "X-Forwarded-Host", "evil.com\r\nX-Injected: 1"},
		{"X-Forwarded-Host with LF injection", "X-Forwarded-Host", "evil.com\nX-Injected: 1"},
		{"X-Forwarded-Host with tab", "X-Forwarded-Host", "evil.com\tpath"},
		{"X-Forwarded-Host with space", "X-Forwarded-Host", "evil.com path"},
		{"X-Forwarded-Host with NUL", "X-Forwarded-Host", "evil.com\x00"},
		{"Forwarded host= with path", "Forwarded", `host="evil.com/path"`},
		{"Forwarded host= with CRLF", "Forwarded", "host=\"evil.com\r\nX-Injected: 1\""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.local/mcp", nil)
			r.Header.Set(tc.header, tc.value)

			got, err := CanonicalizeResourceURL(r, true)
			assert.Empty(t, got, "strict canonicalizer must not return a URL for malformed input")
			require.ErrorIs(t, err, ErrInvalidForwardedHeaders,
				"error should be ErrInvalidForwardedHeaders, got %v", err)
		})
	}
}

func TestCanonicalizeResourceURLStrict_RejectsUnknownForwardedProto(t *testing.T) {
	t.Parallel()

	// An explicitly-empty header value is ambiguous in HTTP semantics
	// (treated as absent by net/http), so it is not covered here; the
	// strict path rejects any *non-empty* unknown scheme.
	cases := []string{
		"gopher",
		"file",
		"javascript",
		"https://evil",
	}
	for _, proto := range cases {
		t.Run("proto="+proto, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://api.example.com/mcp", nil)
			r.Header.Set("X-Forwarded-Proto", proto)

			got, err := CanonicalizeResourceURL(r, true)
			assert.Empty(t, got)
			require.ErrorIs(t, err, ErrInvalidForwardedHeaders,
				"unknown forwarded scheme must be rejected strictly; got err=%v", err)
		})
	}
}

func TestCanonicalizeResourceURLStrict_NilRequest(t *testing.T) {
	t.Parallel()
	got, err := CanonicalizeResourceURL(nil, true)
	assert.Empty(t, got)
	require.ErrorIs(t, err, ErrEmptyResourceURL)
}

func TestCanonicalizeChallengeOrigin_FallsBackOnMalformedForwardedHost(t *testing.T) {
	t.Parallel()

	// The challenge origin is used inside a 401 response where we cannot
	// fail the request. On malformed forwarded input it must still emit
	// a plausible origin from r.Host rather than panic or echo the
	// attacker value.
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://api.example.com/mcp", nil)
	r.Host = advTestAPIHost
	r.Header.Set("X-Forwarded-Host", "evil.com\r\nX-Injected: 1")

	got := CanonicalizeChallengeOrigin(r, true)
	assert.Equal(t, "http://api.example.com/mcp", got,
		"challenge origin must fall back to r.Host when forwarded header is malformed; must never reflect attacker bytes")
	assert.NotContains(t, got, "evil.com")
	assert.NotContains(t, got, "\r")
	assert.NotContains(t, got, "\n")
}

func TestCanonicalizeChallengeOrigin_UsesForwardedWhenValid(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.local/mcp", nil)
	r.Host = "internal.local"
	r.Header.Set("X-Forwarded-Host", "public.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")

	got := CanonicalizeChallengeOrigin(r, true)
	assert.Equal(t, "https://public.example.com/mcp", got)
}

func TestCanonicalizeResourceURL_DefaultIgnoresForwarded(t *testing.T) {
	t.Parallel()

	// With trustProxy=false (the default for any generated server that
	// did not opt into TrustProxyHeaders()), a valid-looking but
	// attacker-controlled X-Forwarded-Host MUST NOT poison the returned
	// URL. This is the central defense for servers reachable directly
	// by clients.
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://api.example.com/mcp", nil)
	r.Host = advTestAPIHost
	r.Header.Set("X-Forwarded-Host", "evil.com")
	r.Header.Set("X-Forwarded-Proto", "https")

	got, err := CanonicalizeResourceURL(r, false)
	require.NoError(t, err)
	assert.Equal(t, "http://api.example.com/mcp", got,
		"with trustProxy=false, forwarded headers must be ignored — the resource URL must reflect only r.Host + r.TLS")
}

func TestCanonicalizeChallengeOrigin_DefaultIgnoresForwarded(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://api.example.com/mcp", nil)
	r.Host = advTestAPIHost
	r.Header.Set("X-Forwarded-Host", "evil.com")

	got := CanonicalizeChallengeOrigin(r, false)
	assert.NotContains(t, got, "evil.com",
		"challenge origin with trustProxy=false must not reflect forwarded-host input")
	assert.Equal(t, "http://api.example.com/mcp", got)
}

func TestCanonicalizeChallengeOrigin_NilRequest(t *testing.T) {
	t.Parallel()
	assert.Empty(t, CanonicalizeChallengeOrigin(nil, true))
}

func TestBuildBearerChallenge_StripsCRLFFromResourceURL(t *testing.T) {
	t.Parallel()

	// Defense in depth: if the challenge builder is handed a URL
	// containing CR/LF (e.g., because the upstream canonicalizer was
	// bypassed or a DSL constant was misconfigured), the resulting
	// header value must not carry raw CR/LF. Without the newline,
	// injected-looking bytes inside the quoted-string cannot terminate
	// the header and forge additional headers — they are inert text.
	challenge := BuildBearerChallenge("https://api.example.com/meta\r\nX-Injected: 1", "read")
	assert.NotContains(t, challenge, "\r", "CR must not survive into header value (header splitting)")
	assert.NotContains(t, challenge, "\n", "LF must not survive into header value (header splitting)")
}

func TestBuildInvalidTokenChallenge_StripsCRLFFromErrorDescription(t *testing.T) {
	t.Parallel()

	challenge := BuildInvalidTokenChallenge("https://api.example.com/meta", "audience mismatch\r\nX-Injected: 1")
	assert.NotContains(t, challenge, "\r")
	assert.NotContains(t, challenge, "\n")
}

func TestWithOAuthChallenge_CaseInsensitiveResourceMetadataDetection(t *testing.T) {
	t.Parallel()

	// Upstream handler sets a differently-cased parameter name. Per
	// RFC 7235, challenge parameter names are case-insensitive, so our
	// "don't overwrite existing challenge" guard must match that.
	cases := []string{
		`Bearer resource_metadata="upstream"`,
		`Bearer Resource_Metadata="upstream"`,
		`Bearer RESOURCE_METADATA="upstream"`,
	}
	for _, existing := range cases {
		t.Run(existing, func(t *testing.T) {
			var called bool
			challenge := ChallengeBuilder(func(*http.Request, string) string {
				called = true
				return `Bearer resource_metadata="ours"`
			})
			handler := WithOAuthChallenge(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("WWW-Authenticate", existing)
				w.WriteHeader(http.StatusUnauthorized)
			}), "/rpc", challenge)

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			handler.ServeHTTP(rec, req)

			assert.False(t, called, "challenge builder must not run when existing header already carries resource_metadata= (any case)")
			assert.Equal(t, existing, rec.Header().Get("WWW-Authenticate"),
				"existing upstream challenge must be preserved verbatim")
		})
	}
}

func TestForwardedParam_QuotedSemicolonInsideValue(t *testing.T) {
	t.Parallel()

	// RFC 7239 quoted-string values may contain `;` without terminating
	// the parameter list. A naive splitter would truncate the host.
	v, ok := forwardedParam(`for=1.2.3.4;host="public.example.com;trailing";proto=https`, "host")
	assert.True(t, ok, "host parameter must be recognized despite `;` inside the quoted value")
	assert.Equal(t, "public.example.com;trailing", v)
}

func TestForwardedParam_QuotedCommaDoesNotSplitElement(t *testing.T) {
	t.Parallel()

	// Commas separate forwarded-elements, but not when they appear
	// inside a quoted-string. The parser must pick the first element
	// honoring that rule.
	v, ok := forwardedParam(`host="a,b";proto=https`, "host")
	assert.True(t, ok)
	assert.Equal(t, "a,b", v)
}

func TestForwardedParam_QuotedPairEscape(t *testing.T) {
	t.Parallel()

	// Quoted-pair `\X` → `X`. A forwarded `host="pu\\blic.example.com"`
	// must decode to `public.example.com`, not the backslash form.
	v, ok := forwardedParam(`host="pu\blic.example.com";proto=https`, "host")
	assert.True(t, ok)
	assert.Equal(t, "public.example.com", v)
}

func TestForwardedParam_AbsentKeyReturnsFalse(t *testing.T) {
	t.Parallel()

	v, ok := forwardedParam(`for=1.2.3.4;proto=https`, "host")
	assert.False(t, ok, "absent key must report not-present")
	assert.Empty(t, v)
}

func TestCanonicalizeChallengeOrigin_NoInjectionFromMalformedHost(t *testing.T) {
	t.Parallel()

	// Full end-to-end: simulate a challenge interceptor reaching the
	// response writer with a malformed X-Forwarded-Host. The resulting
	// WWW-Authenticate header must not contain CR, LF, or the attacker
	// bytes.
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://api.example.com/mcp", nil)
	r.Host = advTestAPIHost
	r.Header.Set("X-Forwarded-Host", "evil.com\r\nX-Injected: 1")

	origin := CanonicalizeChallengeOrigin(r, true)
	challenge := BuildBearerChallenge(origin+"/.well-known/oauth-protected-resource", "read")

	assert.NotContains(t, challenge, "\r")
	assert.NotContains(t, challenge, "\n")
	assert.NotContains(t, challenge, "evil.com")
	assert.True(t, strings.HasPrefix(challenge, `Bearer resource_metadata="http://api.example.com/`),
		"challenge should be rooted at r.Host; got %q", challenge)
}
