package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestClientResolvesRotatingCredentialsPerRequest(t *testing.T) {
	credentialCalls := 0
	var tokens []string
	var accounts []string
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(func(context.Context) (Credentials, error) {
			credentialCalls++
			return Credentials{AccessToken: fmt.Sprintf("token-%d", credentialCalls), AccountID: fmt.Sprintf("account-%d", credentialCalls)}, nil
		}),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			tokens = append(tokens, request.Header.Get("Authorization"))
			accounts = append(accounts, request.Header.Get("chatgpt-account-id"))
			return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
		})},
		Transport: TransportSSE, DefaultModel: "model",
	})
	require.NoError(t, err)
	_, err = client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	_, err = client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	assert.Equal(t, 2, credentialCalls)
	assert.Equal(t, []string{"Bearer token-1", "Bearer token-2"}, tokens)
	assert.Equal(t, []string{"account-1", "account-2"}, accounts)
}

func TestNewDefaultsAndValidatesOptions(t *testing.T) {
	_, err := New(Options{})
	require.ErrorContains(t, err, "credential source is required")
	_, err = New(Options{CredentialSource: CredentialSourceFunc(nil), DefaultModel: "model"})
	require.ErrorContains(t, err, "credential source is required")
	_, err = New(Options{CredentialSource: CredentialSourceFunc(testCredentials)})
	require.ErrorContains(t, err, "default model is required")
	_, err = New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model", Transport: Transport(99)})
	require.ErrorContains(t, err, "invalid transport")
	_, err = New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model", StreamIdleTimeout: -1})
	require.ErrorContains(t, err, "must not be negative")

	client, err := New(Options{CredentialSource: CredentialSourceFunc(testCredentials), DefaultModel: "model"})
	require.NoError(t, err)
	assert.Equal(t, defaultClientVersion, client.version)
	assert.Equal(t, defaultIdleTimeout, client.idleTimeout)
	assert.Equal(t, TransportAuto, client.transport)
}

func TestNewCopiesInjectedClientsAndEnforcesWirePolicy(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	transport := &http.Transport{}
	httpClient := &http.Client{Transport: transport, Jar: jar}
	dialer := &websocket.Dialer{Jar: jar, Subprotocols: []string{"forbidden"}, EnableCompression: true}
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(testCredentials),
		HTTPClient:       httpClient,
		WebSocketDialer:  dialer,
		DefaultModel:     "model",
	})
	require.NoError(t, err)
	assert.NotSame(t, httpClient, client.httpClient)
	assert.NotSame(t, dialer, client.wsDialer)
	require.ErrorIs(t, client.httpClient.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	assert.Nil(t, httpClient.CheckRedirect)
	assert.Same(t, jar, httpClient.Jar)
	assert.Same(t, jar, dialer.Jar)
	assert.Nil(t, client.httpClient.Jar)
	assert.Nil(t, client.wsDialer.Jar)
	clonedTransport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	assert.NotSame(t, transport, clonedTransport)
	assert.False(t, transport.DisableCompression)
	assert.True(t, clonedTransport.DisableCompression)
	assert.Equal(t, []string{"forbidden"}, dialer.Subprotocols)
	assert.True(t, dialer.EnableCompression)
	assert.Empty(t, client.wsDialer.Subprotocols)
	assert.False(t, client.wsDialer.EnableCompression)
}

func TestNewRejectsUnsafeClientVersion(t *testing.T) {
	_, err := New(Options{
		CredentialSource: CredentialSourceFunc(testCredentials),
		DefaultModel:     "model",
		ClientVersion:    "unsafe\nheader",
	})
	require.ErrorContains(t, err, "safe header value")
}

func TestClientRoutesConfiguredModels(t *testing.T) {
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(testCredentials),
		DefaultModel:     "default",
		HighModel:        "high",
		SmallModel:       "small",
	})
	require.NoError(t, err)
	tests := []struct {
		name    string
		request *model.Request
		want    string
	}{
		{name: "default", request: &model.Request{}, want: "default"},
		{name: "high", request: &model.Request{ModelClass: model.ModelClassHighReasoning}, want: "high"},
		{name: "small", request: &model.Request{ModelClass: model.ModelClassSmall}, want: "small"},
		{name: "explicit", request: &model.Request{Model: "explicit", ModelClass: model.ModelClassSmall}, want: "explicit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, client.resolveModelID(tt.request))
		})
	}
}

func TestProviderErrorMetadataCannotExposeCredentials(t *testing.T) {
	header := make(http.Header)
	header.Set("x-request-id", "request-account-1")
	err := normalizeHTTPError(
		http.StatusBadRequest,
		header,
		[]byte(`{"error":{"code":"failure-secret-token"}}`),
		nil,
		Credentials{AccessToken: "secret-token", AccountID: "account-1", Residency: "us"},
	)
	providerErr, ok := model.AsProviderError(err)
	require.True(t, ok)
	assert.Empty(t, providerErr.Code())
	assert.Empty(t, providerErr.RequestID())
	assert.NotContains(t, err.Error(), "secret-token")
	assert.NotContains(t, err.Error(), "account-1")
}
