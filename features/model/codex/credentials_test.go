package codex

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCredentialsExplicitValuesWin(t *testing.T) {
	token := jwtToken(`{"https://api.openai.com/auth":{"chatgpt_account_id":"jwt-account","chatgpt_data_residency":"eu"}}`)
	credentials, err := resolveCredentials(context.Background(), CredentialSourceFunc(func(context.Context) (Credentials, error) {
		return Credentials{AccessToken: token, AccountID: "explicit-account", Residency: "ca"}, nil
	}))
	require.NoError(t, err)
	assert.Equal(t, "explicit-account", credentials.AccountID)
	assert.Equal(t, "ca", credentials.Residency)
}

func TestResolveCredentialsDerivesJWTAccountAndResidency(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{name: "data residency wins", json: `{"https://api.openai.com/auth":{"chatgpt_account_id":"account","chatgpt_data_residency":"eu","chatgpt_compute_residency":"us"}}`, want: "eu"},
		{name: "compute fallback", json: `{"https://api.openai.com/auth":{"chatgpt_account_id":"account","chatgpt_compute_residency":"us"}}`, want: "us"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credentials, err := resolveCredentials(context.Background(), CredentialSourceFunc(func(context.Context) (Credentials, error) {
				return Credentials{AccessToken: jwtToken(tt.json)}, nil
			}))
			require.NoError(t, err)
			assert.Equal(t, "account", credentials.AccountID)
			assert.Equal(t, tt.want, credentials.Residency)
		})
	}
}

func TestResolveCredentialsRejectsMalformedAndOversizedJWTs(t *testing.T) {
	tokens := []string{
		"opaque",
		"a.not-base64.c",
		"." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`)) + ".signature",
		"header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account"}}`)) + ".",
		"a." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{}} trailing`)) + ".c",
		jwtToken(`{"https://api.openai.com/auth":{"chatgpt_account_id":"first"},"https://api.openai.com/auth":{"chatgpt_account_id":"second"}}`),
	}
	for _, token := range tokens {
		_, err := resolveCredentials(context.Background(), CredentialSourceFunc(func(context.Context) (Credentials, error) {
			return Credentials{AccessToken: token}, nil
		}))
		require.EqualError(t, err, "codex: account or workspace ID is required")
	}
}

func TestResolveCredentialsRejectsOversizedTokenBeforeExplicitClaims(t *testing.T) {
	_, err := resolveCredentials(context.Background(), CredentialSourceFunc(func(context.Context) (Credentials, error) {
		return Credentials{
			AccessToken: strings.Repeat("x", maxJWTBytes+1),
			AccountID:   "explicit-account",
			Residency:   "us",
		}, nil
	}))
	require.EqualError(t, err, "codex: access token exceeds 64 KiB")
}

func TestResolveCredentialsRedactsSourceErrors(t *testing.T) {
	_, err := resolveCredentials(context.Background(), CredentialSourceFunc(func(context.Context) (Credentials, error) {
		return Credentials{}, errors.New("failed with secret-token")
	}))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-token")
}

func TestResolveCredentialsPreservesContextSentinelsWithoutSourceText(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		_, err := resolveCredentials(context.Background(), CredentialSourceFunc(func(context.Context) (Credentials, error) {
			return Credentials{}, fmt.Errorf("secret source detail: %w", sentinel)
		}))
		require.ErrorIs(t, err, sentinel)
		assert.NotContains(t, err.Error(), "secret source detail")
	}
}

func jwtToken(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString([]byte("signature"))
}
