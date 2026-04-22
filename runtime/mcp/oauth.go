// Package mcp OAuth helpers (oauth.go) provide protected-resource
// formatting shared by generated MCP servers and hand-written
// middleware. Pure formatting and URL canonicalization — no token
// validation happens here.
package mcp

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ErrInvalidForwardedHeaders signals that a forwarded-identity header was
// present on the request but failed validation (contained control or
// delimiter characters, or was otherwise unsafe to embed in a URL).
// Callers that derive the RFC 8707 canonical resource URI from request
// context should treat this as a 400 Bad Request rather than silently
// falling back to `r.Host`: a malformed forwarded header signals either a
// misconfigured proxy or a client probing for injection vectors, and
// either way the safe response is to reject the request.
var ErrInvalidForwardedHeaders = errors.New("mcp/oauth: invalid forwarded identity header")

// ErrEmptyResourceURL signals that CanonicalizeResourceURL could not
// derive a non-empty scheme+host for the request. Callers should treat
// this as a 400 Bad Request because RFC 9728 requires `resource` to be a
// fully-qualified URI.
var ErrEmptyResourceURL = errors.New("mcp/oauth: cannot derive canonical resource URL")

// ProtectedResourceMetadataPrefix is the RFC 9728 well-known prefix for
// OAuth 2.0 protected-resource metadata.
const ProtectedResourceMetadataPrefix = "/.well-known/oauth-protected-resource"

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// ProtectedResourceMetadataPath returns the RFC 9728 §3.1 path-suffixed
// metadata path for a server mounted at mountPath. The root alias
// "/.well-known/oauth-protected-resource" is produced when mountPath is
// empty or "/".
func ProtectedResourceMetadataPath(mountPath string) string {
	cleaned := strings.TrimRight(mountPath, "/")
	if cleaned == "" {
		return ProtectedResourceMetadataPrefix
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return ProtectedResourceMetadataPrefix + cleaned
}

// CanonicalizeResourceURL derives the RFC 8707 canonical resource URI
// from an incoming request.
//
// When trustProxy is true, the function honors X-Forwarded-Proto and
// X-Forwarded-Host (and RFC 7239 Forwarded) so the value reflects the
// client-visible URL behind a trusted reverse proxy; a forwarded header
// present but malformed is rejected with ErrInvalidForwardedHeaders.
//
// When trustProxy is false (the default for a generated server without
// TrustProxyHeaders() in the DSL), forwarded headers are ignored
// entirely and the origin is derived from r.Host + r.TLS only. This is
// the safe default for any server reachable directly by clients:
// without it, an attacker with direct access controls the PRM
// `resource` field advertised to clients.
//
// Returns ErrEmptyResourceURL when no scheme+host can be derived.
// Callers should surface errors as 400 Bad Request rather than emitting
// a PRM document or challenge URL built from attacker-influenced or
// unusable inputs.
//
// Operators who cannot vouch for forwarded headers should either leave
// trustProxy at its default or declare the resource identifier
// explicitly in the MCP DSL; generated code uses the declared value
// and does not call this function in the pinned case.
func CanonicalizeResourceURL(r *http.Request, trustProxy bool) (string, error) {
	if r == nil {
		return "", ErrEmptyResourceURL
	}
	var scheme, host string
	if trustProxy {
		s, err := forwardedSchemeStrict(r)
		if err != nil {
			return "", err
		}
		h, err := forwardedHostStrict(r)
		if err != nil {
			return "", err
		}
		scheme, host = s, h
	} else {
		scheme = schemeHTTP
		if r.TLS != nil {
			scheme = schemeHTTPS
		}
		host = r.Host
	}
	if scheme == "" || host == "" {
		return "", ErrEmptyResourceURL
	}
	host = strings.ToLower(host)
	host = stripDefaultPort(scheme, host)
	u := &url.URL{Scheme: scheme, Host: host, Path: canonicalPath(r.URL.Path)}
	return u.String(), nil
}

// CanonicalizeChallengeOrigin derives an origin URL suitable for embedding
// in a WWW-Authenticate `resource_metadata` parameter. Unlike the strict
// CanonicalizeResourceURL, this function never returns an error: if
// forwarded headers are malformed (or the caller did not opt into
// trusting them), it falls back to the request scheme and Host header so
// the challenge still points at some reachable origin.
//
// This fallback is deliberate and narrow: a challenge is a formatting
// artifact inside a 401 response, not an identity claim. Emitting a
// slightly-wrong URL back to a client whose request carried malformed
// proxy headers is better than emitting nothing. Do not use this function
// to populate the PRM `resource` field — use CanonicalizeResourceURL
// there so the request fails loudly on malformed input.
func CanonicalizeChallengeOrigin(r *http.Request, trustProxy bool) string {
	if r == nil {
		return ""
	}
	if u, err := CanonicalizeResourceURL(r, trustProxy); err == nil {
		return u
	}
	scheme := schemeHTTP
	if r.TLS != nil {
		scheme = schemeHTTPS
	}
	host := strings.ToLower(stripDefaultPort(scheme, r.Host))
	if host == "" {
		return ""
	}
	u := &url.URL{Scheme: scheme, Host: host, Path: canonicalPath(r.URL.Path)}
	return u.String()
}

// WriteUnauthorized writes a spec-compliant 401 response with a Bearer
// challenge. The resource_metadata parameter points the client at the
// Protected Resource Metadata document; scope is the space-delimited
// list of scopes the server advertises. When scope is empty the scope
// parameter is omitted entirely (RFC 6750 does not require it).
func WriteUnauthorized(w http.ResponseWriter, resourceMetadataURL, scope string) {
	challenge := BuildBearerChallenge(resourceMetadataURL, scope)
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}

// BuildBearerChallenge formats the WWW-Authenticate header value for an
// OAuth 2.0 Bearer challenge per RFC 6750 §3. resourceMetadataURL is
// required; scope is appended only when non-empty.
func BuildBearerChallenge(resourceMetadataURL, scope string) string {
	var b strings.Builder
	b.WriteString(`Bearer resource_metadata="`)
	b.WriteString(escapeHeaderQuoted(resourceMetadataURL))
	b.WriteString(`"`)
	if scope != "" {
		b.WriteString(`, scope="`)
		b.WriteString(escapeHeaderQuoted(scope))
		b.WriteString(`"`)
	}
	return b.String()
}

// BuildInvalidTokenChallenge formats the WWW-Authenticate header for an
// RFC 6750 §3 invalid_token response (used for audience mismatches,
// expired tokens, and revoked tokens). errorDescription is optional and
// should be a short human-readable string safe to surface to clients.
func BuildInvalidTokenChallenge(resourceMetadataURL, errorDescription string) string {
	var b strings.Builder
	b.WriteString(`Bearer resource_metadata="`)
	b.WriteString(escapeHeaderQuoted(resourceMetadataURL))
	b.WriteString(`", error="invalid_token"`)
	if errorDescription != "" {
		b.WriteString(`, error_description="`)
		b.WriteString(escapeHeaderQuoted(errorDescription))
		b.WriteString(`"`)
	}
	return b.String()
}

// WriteInvalidToken writes a 401 response with the invalid_token Bearer
// challenge. Consumers call this from a verifier wrapper when a
// decoded token carries the wrong audience, is expired, or is revoked.
func WriteInvalidToken(w http.ResponseWriter, resourceMetadataURL, errorDescription string) {
	challenge := BuildInvalidTokenChallenge(resourceMetadataURL, errorDescription)
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
}

// forwardedSchemeStrict returns the client-visible scheme after validating
// forwarded headers. A forwarded header that is present but malformed
// (unknown scheme, control/delimiter chars) returns ErrInvalidForwardedHeaders
// rather than falling back silently.
func forwardedSchemeStrict(r *http.Request) (string, error) {
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		s := strings.ToLower(strings.TrimSpace(firstCSV(v)))
		if !isValidScheme(s) {
			return "", ErrInvalidForwardedHeaders
		}
		return s, nil
	}
	if v, ok := forwardedParam(r.Header.Get("Forwarded"), "proto"); ok {
		s := strings.ToLower(v)
		if !isValidScheme(s) {
			return "", ErrInvalidForwardedHeaders
		}
		return s, nil
	}
	if r.TLS != nil {
		return schemeHTTPS, nil
	}
	return schemeHTTP, nil
}

// forwardedHostStrict returns the client-visible host after validating
// forwarded headers. A forwarded header that is present but malformed
// returns ErrInvalidForwardedHeaders rather than falling back to
// r.Host — a malformed proxy header is a signal, not noise to ignore.
func forwardedHostStrict(r *http.Request) (string, error) {
	if v := r.Header.Get("X-Forwarded-Host"); v != "" {
		host := strings.TrimSpace(firstCSV(v))
		if !isValidHostPort(host) {
			return "", ErrInvalidForwardedHeaders
		}
		return host, nil
	}
	if v, ok := forwardedParam(r.Header.Get("Forwarded"), "host"); ok {
		if !isValidHostPort(v) {
			return "", ErrInvalidForwardedHeaders
		}
		return v, nil
	}
	return r.Host, nil
}

// isValidScheme returns true for the only schemes MCP transports speak.
// HTTP and HTTPS cover every MCP Streamable HTTP and JSON-RPC deployment;
// anything else from a forwarded header is a misconfiguration or
// injection attempt.
func isValidScheme(s string) bool {
	return s == schemeHTTP || s == schemeHTTPS
}

// isValidHostPort returns true when v looks like an RFC 3986 host[:port]
// authority component and contains no characters that would let it escape
// a URL context or inject into a response header. This deliberately
// rejects userinfo (`@`), path/query/fragment markers (`/`, `?`, `#`),
// whitespace, and any ASCII control characters (including CR/LF).
func isValidHostPort(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch c {
		case '/', '?', '#', '@', ' ', '\t', '\r', '\n':
			return false
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// forwardedParam parses an RFC 7239 Forwarded header value and returns the
// value associated with key in the first forwarded-element. The second
// return is true when the key was present, regardless of whether the
// value is empty — callers rely on that distinction to tell "no forwarded
// header" from "present but empty," which matter differently for strict
// validation.
//
// The parser is intentionally small. It handles quoted-string values
// (including semicolons inside quotes) and quoted-pair escapes, which
// covers legitimate proxy output. It does not attempt to validate every
// RFC 7239 nuance; isValidHostPort / isValidScheme do that downstream.
func forwardedParam(header, key string) (string, bool) {
	if header == "" {
		return "", false
	}
	first := firstForwardedElement(header)
	for _, part := range splitForwardedParams(first) {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(kv[0], key) {
			return unquoteForwardedValue(kv[1]), true
		}
	}
	return "", false
}

// firstForwardedElement returns the first element of an RFC 7239
// Forwarded header, where elements are comma-separated but commas inside
// quoted strings must not split. A naive strings.IndexByte(',') is wrong
// here: `host="a,b"` is one element with a comma in its quoted host.
func firstForwardedElement(header string) string {
	var b strings.Builder
	inQuotes := false
	escaped := false
	for i := 0; i < len(header); i++ {
		c := header[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && inQuotes {
			b.WriteByte(c)
			escaped = true
			continue
		}
		if c == '"' {
			inQuotes = !inQuotes
			b.WriteByte(c)
			continue
		}
		if c == ',' && !inQuotes {
			break
		}
		b.WriteByte(c)
	}
	return strings.TrimSpace(b.String())
}

// splitForwardedParams splits a single forwarded-element on `;` while
// respecting quoted strings and quoted-pair escapes. Necessary because
// `host="public;injected"` must parse as one parameter, not two.
func splitForwardedParams(element string) []string {
	var out []string
	var b strings.Builder
	inQuotes := false
	escaped := false
	for i := 0; i < len(element); i++ {
		c := element[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && inQuotes {
			b.WriteByte(c)
			escaped = true
			continue
		}
		if c == '"' {
			inQuotes = !inQuotes
			b.WriteByte(c)
			continue
		}
		if c == ';' && !inQuotes {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	out = append(out, b.String())
	return out
}

// unquoteForwardedValue strips surrounding DQUOTEs and resolves
// quoted-pair escapes (`\X` → `X`) per RFC 7239 §4. Unquoted tokens are
// returned unchanged.
func unquoteForwardedValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return v
	}
	inner := v[1 : len(v)-1]
	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '\\' && i+1 < len(inner) {
			b.WriteByte(inner[i+1])
			i++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func firstCSV(v string) string {
	first, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(first)
}

func canonicalPath(p string) string {
	if p == "" {
		return ""
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

func stripDefaultPort(scheme, host string) string {
	switch scheme {
	case schemeHTTP:
		return strings.TrimSuffix(host, ":80")
	case schemeHTTPS:
		return strings.TrimSuffix(host, ":443")
	default:
		return host
	}
}

// escapeHeaderQuoted escapes a value for embedding inside an RFC 7230
// quoted-string header parameter. It escapes `"` and `\` per the spec,
// and strips CR and LF entirely — those would split the header and let
// untrusted input forge additional response headers. Stripping rather
// than escaping matches what net/http's own header validation does: the
// characters are simply not permitted in a response header value, so
// removing them is equivalent to rejecting the offending byte.
func escapeHeaderQuoted(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\r", "",
		"\n", "",
	)
	return replacer.Replace(s)
}

// ChallengeBuilder formats a WWW-Authenticate header value for a request
// against the server mounted at mountPath. Generated packages export
// OAuthChallengeHeader matching this signature.
type ChallengeBuilder func(r *http.Request, mountPath string) string

// WithOAuthChallenge wraps a handler so that 401 responses missing a
// resource_metadata-carrying WWW-Authenticate header are augmented with
// the spec-compliant challenge built by challenge. Responses that
// already include resource_metadata are left untouched so consumer
// middleware can still override the default.
func WithOAuthChallenge(handler http.Handler, mountPath string, challenge ChallengeBuilder) http.Handler {
	if handler == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		interceptor := &challengeInterceptor{
			ResponseWriter: w,
			request:        r,
			mountPath:      mountPath,
			challenge:      challenge,
		}
		handler.ServeHTTP(interceptor, r)
	})
}

type challengeInterceptor struct {
	http.ResponseWriter
	request     *http.Request
	mountPath   string
	challenge   ChallengeBuilder
	wroteHeader bool
}

func (c *challengeInterceptor) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	if status == http.StatusUnauthorized && c.challenge != nil {
		existing := c.Header().Get("WWW-Authenticate")
		// RFC 7235 challenge parameter names are case-insensitive, so
		// `Resource_Metadata=` from an upstream handler should still
		// prevent us from overwriting the challenge.
		if !strings.Contains(strings.ToLower(existing), "resource_metadata=") {
			c.Header().Set("WWW-Authenticate", c.challenge(c.request, c.mountPath))
		}
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *challengeInterceptor) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(p)
}

// Flush passes through to the wrapped ResponseWriter if it supports
// flushing. Required because MCP streams rely on SSE-style flushing.
func (c *challengeInterceptor) Flush() {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	if flusher, ok := c.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
