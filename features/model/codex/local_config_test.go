package codex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveCodexCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		auth    string
		wantRun bool
		wantErr string
	}{
		{name: "missing credentials skip"},
		{name: "explicit opt out", env: map[string]string{"CODEX_INTEGRATION": "0"}, auth: "invalid"},
		{name: "CI skips even when required", env: map[string]string{"CI": "true", "CODEX_INTEGRATION": "1"}, auth: "invalid"},
		{name: "GitHub skips even when required", env: map[string]string{"GITHUB_ACTIONS": "true", "CODEX_INTEGRATION": "1"}, auth: "invalid"},
		{name: "required credentials missing", env: map[string]string{"CODEX_INTEGRATION": "1"}, wantErr: "credentials are required"},
		{name: "invalid opt in", env: map[string]string{"CODEX_INTEGRATION": "invalid"}, wantErr: "CODEX_INTEGRATION must be 0 or 1"},
		{name: "environment credentials", env: map[string]string{"CODEX_ACCESS_TOKEN": " token ", "CODEX_ACCOUNT_ID": " account ", "CODEX_RESIDENCY": " us "}, wantRun: true},
		{name: "environment wins over file", env: map[string]string{"CODEX_ACCESS_TOKEN": "token", "CODEX_ACCOUNT_ID": "account", "CODEX_RESIDENCY": "us"}, auth: "invalid", wantRun: true},
		{name: "partial environment fails", env: map[string]string{"CODEX_ACCESS_TOKEN": "secret"}, wantErr: "set both CODEX_ACCESS_TOKEN and CODEX_ACCOUNT_ID"},
		{name: "local credentials", env: map[string]string{"CODEX_RESIDENCY": "us"}, auth: `{"tokens":{"access_token":"token","account_id":"account"}}`, wantRun: true},
		{name: "malformed local credentials", auth: "secret", wantErr: "cannot decode local Codex auth.json"},
		{name: "API key login skips", auth: `{"OPENAI_API_KEY":"secret"}`},
		{name: "required subscription credentials", env: map[string]string{"CODEX_INTEGRATION": "1"}, auth: `{"tokens":{}}`, wantErr: "credentials are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"CI", "GITHUB_ACTIONS", "CODEX_INTEGRATION", "CODEX_ACCESS_TOKEN", "CODEX_ACCOUNT_ID", "CODEX_RESIDENCY"} {
				t.Setenv(key, "")
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			authDir := t.TempDir()
			t.Setenv("CODEX_HOME", authDir)
			if tc.auth != "" {
				require.NoError(t, os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(tc.auth), 0o600))
			}
			credentials, skip, err := liveCodexCredentials()
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.NotContains(t, err.Error(), "secret")
				return
			}
			require.NoError(t, err)
			if !tc.wantRun {
				assert.NotEmpty(t, skip)
				assert.Empty(t, credentials.AccessToken)
				return
			}
			assert.Empty(t, skip)
			assert.Equal(t, "token", credentials.AccessToken)
			assert.Equal(t, "account", credentials.AccountID)
			assert.Equal(t, "us", credentials.Residency)
		})
	}
}
