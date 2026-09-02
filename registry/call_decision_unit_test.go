package registry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCallRejectionValidatesClosedWireResult(t *testing.T) {
	t.Parallel()

	for _, kind := range []callRejectionKind{
		callRejectionNotFound,
		callRejectionValidation,
		callRejectionUnavailable,
	} {
		rejection, err := parseCallRejection([]any{string(kind), "rejected"})
		require.NoError(t, err)
		require.Equal(t, callRejection{kind: kind, message: "rejected"}, rejection)
		require.Equal(t, "rejected", (&callRejectedError{rejection: rejection}).Error())
	}

	for _, value := range [][]any{
		nil,
		{"validation_error"},
		{7, "rejected"},
		{"future_kind", "rejected"},
		{"validation_error", 7},
		{"validation_error", ""},
	} {
		_, err := parseCallRejection(value)
		require.Error(t, err)
	}
}

func TestRedisResultDecodersRejectAmbiguousTypes(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(4), mustRedisResultInt64(t, int64(4)))
	require.Equal(t, int64(5), mustRedisResultInt64(t, int(5)))
	_, err := redisResultInt64("5")
	require.ErrorContains(t, err, "unexpected Redis integer reply")

	value, err := redisResultString("stored", "field")
	require.NoError(t, err)
	require.Equal(t, "stored", value)
	_, err = redisResultString([]byte("stored"), "field")
	require.ErrorContains(t, err, "invalid field")

	valueBool, err := redisResultBool("0", "flag")
	require.NoError(t, err)
	require.False(t, valueBool)
	valueBool, err = redisResultBool("1", "flag")
	require.NoError(t, err)
	require.True(t, valueBool)
	_, err = redisResultBool("true", "flag")
	require.ErrorContains(t, err, "invalid flag")
}

func TestCallAdmissionStoreDerivesStableIsolatedKeys(t *testing.T) {
	t.Parallel()

	store := newCallAdmissionStore(nil, "test-registry")
	first := store.callKey("run-a/call-1")
	second := store.callKey("run-a/call-2")
	require.NotEqual(t, first, second)
	require.Contains(t, first, "registry:test-registry:call:")
	require.Equal(
		t,
		"registry:test-registry:claimed-call-settlement:lease:token:provider\x00incarnation",
		store.leaseSettlementKey("token", "provider\x00incarnation"),
	)
}

func mustRedisResultInt64(t *testing.T, value any) int64 {
	t.Helper()

	result, err := redisResultInt64(value)
	require.NoError(t, err)
	return result
}
