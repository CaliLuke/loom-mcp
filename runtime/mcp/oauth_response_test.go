package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/stretchr/testify/assert"
)

func TestOAuthRejectionResponses(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		write                   func(http.ResponseWriter, string, string)
		detail, challenge, body string
	}{
		{"missing credentials", mcp.WriteUnauthorized, "read write", `Bearer resource_metadata="https://example.test/meta", scope="read write"`, `{"error":"unauthorized"}`},
		{"missing scope", mcp.WriteUnauthorized, "", `Bearer resource_metadata="https://example.test/meta"`, `{"error":"unauthorized"}`},
		{"invalid token", mcp.WriteInvalidToken, "expired", `Bearer resource_metadata="https://example.test/meta", error="invalid_token", error_description="expired"`, `{"error":"invalid_token"}`},
		{"escaped description", mcp.WriteInvalidToken, "bad \"token\"\r\n", `Bearer resource_metadata="https://example.test/meta", error="invalid_token", error_description="bad \"token\""`, `{"error":"invalid_token"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			tc.write(response, "https://example.test/meta", tc.detail)
			assert.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
			assert.Equal(t, tc.challenge, response.Header().Get("WWW-Authenticate"))
			assert.JSONEq(t, tc.body, response.Body.String())
		})
	}
}
