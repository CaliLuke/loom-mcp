// Package mcp OAuth helpers (oauth.go) provide protected-resource
// formatting shared by generated MCP servers and hand-written
// middleware. Pure formatting and URL canonicalization — no token
// validation happens here.
package mcp

import (
	"net/http"
	"net/url"
	"strings"
)

// ProtectedResourceMetadataPrefix is the RFC 9728 well-known prefix for
// OAuth 2.0 protected-resource metadata.
const ProtectedResourceMetadataPrefix = "/.well-known/oauth-protected-resource"

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
// from an incoming request. It honors X-Forwarded-Proto and
// X-Forwarded-Host (and RFC 7239 Forwarded) so the value reflects the
// client-visible URL when the server sits behind a reverse proxy the
// operator trusts. Operators who cannot vouch for those headers should
// declare the resource identifier explicitly in the MCP DSL.
func CanonicalizeResourceURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme := forwardedScheme(r)
	host := forwardedHost(r)
	path := canonicalPath(r.URL.Path)
	if scheme == "" || host == "" {
		return ""
	}
	host = strings.ToLower(host)
	host = stripDefaultPort(scheme, host)
	u := &url.URL{Scheme: scheme, Host: host, Path: path}
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

func forwardedScheme(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		return strings.ToLower(strings.TrimSpace(firstCSV(v)))
	}
	if v := forwardedParam(r.Header.Get("Forwarded"), "proto"); v != "" {
		return strings.ToLower(v)
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func forwardedHost(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-Host"); v != "" {
		return strings.TrimSpace(firstCSV(v))
	}
	if v := forwardedParam(r.Header.Get("Forwarded"), "host"); v != "" {
		return v
	}
	return r.Host
}

func forwardedParam(header, key string) string {
	if header == "" {
		return ""
	}
	first := firstCSV(header)
	for _, part := range strings.Split(first, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(kv[0], key) {
			return strings.Trim(kv[1], `"`)
		}
	}
	return ""
}

func firstCSV(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return strings.TrimSpace(v)
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
	case "http":
		return strings.TrimSuffix(host, ":80")
	case "https":
		return strings.TrimSuffix(host, ":443")
	default:
		return host
	}
}

func escapeHeaderQuoted(s string) string {
	// RFC 7230: quoted-string escapes " and \.
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(s)
}
