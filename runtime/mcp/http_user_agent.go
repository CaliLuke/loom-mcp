package mcp

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
)

type userAgentTransport struct {
	base  http.RoundTripper
	value string
}

const (
	mcpModulePath         = "github.com/CaliLuke/loom-mcp/v2"
	mcpDevelopmentVersion = "dev"
)

// RoundTrip adds identity to a private request before host transport handling.
func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if cloned.Header == nil {
		cloned.Header = make(http.Header)
	}
	if _, present := cloned.Header["User-Agent"]; !present {
		for name, values := range cloned.Header {
			if strings.EqualFold(name, "User-Agent") {
				cloned.Header["User-Agent"] = values
				delete(cloned.Header, name)
				present = true
				break
			}
		}
		if !present {
			cloned.Header.Set("User-Agent", t.value)
		}
	}
	return t.transport().RoundTrip(cloned)
}

// CloseIdleConnections forwards explicit closure to the host transport.
func (t *userAgentTransport) CloseIdleConnections() {
	if closer, ok := t.transport().(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// mcpIdentityClient preserves host client policy and installs identity on private requests.
func mcpIdentityClient(client *http.Client, value string) (*http.Client, error) {
	if value == "" {
		info, _ := debug.ReadBuildInfo()
		value = "loom-mcp/" + mcpLibraryVersion(info)
	}
	for _, b := range []byte(value) {
		if (b < ' ' && b != '\t') || b == 127 {
			return nil, fmt.Errorf("invalid HTTP User-Agent header value")
		}
	}
	cloned := *client
	cloned.Transport = &userAgentTransport{base: client.Transport, value: value}
	return &cloned, nil
}

// mcpLibraryVersion excludes host metadata and local replacement paths.
func mcpLibraryVersion(info *debug.BuildInfo) string {
	if info == nil {
		return mcpDevelopmentVersion
	}
	var module *debug.Module
	if info.Main.Path == mcpModulePath {
		module = &info.Main
	} else {
		for _, dependency := range info.Deps {
			if dependency.Path == mcpModulePath {
				module = dependency
				break
			}
		}
	}
	if module == nil {
		return mcpDevelopmentVersion
	}
	if module.Replace != nil {
		module = module.Replace
	}
	version := module.Version
	if version == "" || version == "(devel)" {
		return mcpDevelopmentVersion
	}
	for _, b := range []byte(version) {
		if b <= ' ' || b >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(b)) {
			return mcpDevelopmentVersion
		}
	}
	return version
}

func (t *userAgentTransport) transport() http.RoundTripper {
	if t.base == nil {
		return http.DefaultTransport
	}
	return t.base
}
