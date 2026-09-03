package sdkbridge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"encoding/json/jsontext"
	json "encoding/json/v2"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	disableLocalhostProtection = mcpGoDebugValue("disablelocalhostprotection") == "1"
	disableContentTypeCheck    = mcpGoDebugValue("disablecontenttypecheck") == "1"
)

type jsonRPCRequestEnvelope struct {
	ID     jsontext.Value `json:"id"`
	Method jsontext.Value `json:"method"`
}

func requestAllowsBodyInspection(r *http.Request, opts *mcpsdk.StreamableHTTPOptions) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if !opts.DisableLocalhostProtection && !disableLocalhostProtection {
		if localAddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && localAddr != nil && loopbackAddress(localAddr.String()) && !loopbackAddress(r.Host) {
			return false
		}
	}
	if !disableContentTypeCheck {
		mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" || parameters == nil {
			return false
		}
	}
	jsonAccepted := false
	streamAccepted := false
	for _, value := range r.Header.Values("Accept") {
		for _, mediaRange := range strings.Split(value, ",") {
			parts := strings.SplitN(mediaRange, ";", 2)
			switch strings.ToLower(strings.TrimSpace(parts[0])) {
			case "application/json", "application/*":
				jsonAccepted = true
			case "text/event-stream", "text/*":
				streamAccepted = true
			case "*/*":
				jsonAccepted = true
				streamAccepted = true
			}
		}
	}
	if !jsonAccepted || !streamAccepted {
		return false
	}
	switch r.Header.Get("MCP-Protocol-Version") {
	case "", "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", "2026-07-28":
		return true
	default:
		return false
	}
}

func loopbackAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host = strings.Trim(address, "[]")
	} else if port == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return parsed.IsLoopback()
}

func mcpGoDebugValue(name string) string {
	value := ""
	for _, part := range strings.Split(os.Getenv("MCPGODEBUG"), ",") {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) == 2 && strings.TrimSpace(pair[0]) == name {
			value = strings.TrimSpace(pair[1])
		}
	}
	return value
}

func rejectNullRequestID(w http.ResponseWriter, r *http.Request, maxBodyBytes int64) bool {
	if r == nil || r.Method != http.MethodPost || r.Body == nil {
		return false
	}
	var reader io.Reader = r.Body
	if maxBodyBytes > 0 {
		reader = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	}
	body, err := io.ReadAll(reader)
	closeErr := r.Body.Close()
	if err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, fmt.Sprintf("request body exceeds %d bytes", maxBytesErr.Limit), http.StatusRequestEntityTooLarge)
			return true
		}
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var envelope jsonRPCRequestEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	if len(envelope.Method) == 0 || !bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Invalid Request: request id must not be null"}}`, http.StatusBadRequest)
	return true
}
