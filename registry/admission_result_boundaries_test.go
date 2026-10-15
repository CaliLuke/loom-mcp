package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmissionRejectsCorruptRedisResultWithoutReplay(t *testing.T) {
	store := newCallAdmissionStore(nil, "registry")
	token := strings.Repeat("a", 64)
	baseline := []any{int64(0), "digest", "1000", "2000", "0", "", token, "0", "", "0", string(outcomeUnknownPayload(token, "call")), "", "", ""}
	admission, err := store.parseResult("tools", "key", "digest", "call", baseline)
	require.NoError(t, err)
	assert.Equal(t, time.UnixMilli(1000), admission.executionDeadline)
	assert.Equal(t, time.UnixMilli(2000), admission.expiresAt)
	assert.Equal(t, token, admission.registrationToken)
	for field := 2; field < len(baseline); field++ {
		t.Run(fmt.Sprintf("nonstring field %d", field), func(t *testing.T) {
			values := append([]any(nil), baseline...)
			values[field] = nil
			got, err := store.parseResult("tools", "key", "digest", "call", values)
			require.Error(t, err)
			assert.Empty(t, got.registrationToken, "invalid tuples must not expose replay authority")
		})
	}
	for _, tc := range []struct {
		name  string
		field int
		value string
	}{
		{"invalid deadline", 2, "tomorrow"},
		{"zero deadline", 2, "0"},
		{"noncanonical deadline", 2, "01000"},
		{"invalid expiration", 3, "tomorrow"},
		{"expired history", 3, "1000"},
		{"noncanonical expiration", 3, "02000"},
		{"invalid terminal flag", 4, "true"},
		{"missing terminal event", 4, "1"},
		{"nonterminal event", 5, "1-0"},
		{"invalid token", 6, "bad"},
		{"invalid published flag", 7, "true"},
		{"overload event without retry", 8, "1-0"},
		{"invalid retry", 9, "later"},
		{"negative retry", 9, "-1"},
		{"retry without event", 9, "1"},
		{"noncanonical retry", 9, "00"},
		{"malformed fallback", 10, "{"},
		{"invalid fallback", 10, "{}"},
		{"wrong fallback identity", 10, string(outcomeUnknownPayload(token, "other"))},
		{"unexpected terminal digest", 11, "digest"},
		{"unexpected terminal payload", 12, "{}"},
		{"unexpected terminal cause", 13, "provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := append([]any(nil), baseline...)
			values[tc.field] = tc.value
			got, err := store.parseResult("tools", "key", "digest", "call", values)
			require.Error(t, err)
			assert.Empty(t, got.registrationToken)
		})
	}
}

func TestAdmissionTerminalReplayRequiresMatchingCauseAndIntegrity(t *testing.T) {
	token := strings.Repeat("a", 64)
	store := newCallAdmissionStore(nil, "registry")
	for _, tc := range []struct {
		cause string
		code  string
	}{
		{"provider", "failed"},
		{"stale_admission", toolregistry.ToolErrorCodeStaleRegistration},
		{"execution_deadline", toolregistry.ToolErrorCodeOutcomeUnknown},
		{"provider_lease_lost", toolregistry.ToolErrorCodeOutcomeUnknown},
		{"provider_lease_released", toolregistry.ToolErrorCodeOutcomeUnknown},
	} {
		t.Run(tc.cause, func(t *testing.T) {
			payload, err := json.Marshal(toolregistry.NewToolResultErrorMessage(token, "call", tc.code, "failure"))
			require.NoError(t, err)
			digest := redisTerminalDigest(payload)
			if tc.cause == "provider" || tc.cause == "stale_admission" {
				sum := sha256.Sum256(payload)
				digest = hex.EncodeToString(sum[:])
			}
			values := []any{int64(0), "request", "1000", "2000", "1", "1-0", token, "1", "", "0", string(outcomeUnknownPayload(token, "call")), digest, string(payload), tc.cause}
			admitted, err := store.parseResult("tools", "key", "request", "call", values)
			require.NoError(t, err)
			assert.True(t, admitted.terminal)
			assert.Equal(t, "1-0", admitted.terminalEventID)
			values[11] = "tampered"
			_, err = store.parseResult("tools", "key", "request", "call", values)
			require.ErrorContains(t, err, "digest")
			values[11] = digest
			values[13] = "unknown"
			_, err = store.parseResult("tools", "key", "request", "call", values)
			require.ErrorContains(t, err, "terminal cause")
		})
	}
}
