package policy

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCapsStateUnmarshalJSONMigratesHistoricalRecoveryFields(t *testing.T) {
	t.Parallel()

	const payload = `{
		"MaxToolCalls": 6,
		"RemainingToolCalls": 4,
		"MaxConsecutiveFailedToolCalls": 5,
		"RemainingConsecutiveFailedToolCalls": 2,
		"ExpiresAt": "2027-02-03T04:05:06Z"
	}`
	var caps CapsState
	require.NoError(t, json.Unmarshal([]byte(payload), &caps))
	require.Equal(t, CapsState{
		MaxToolCalls:           6,
		RemainingToolCalls:     4,
		MaxRecoveryTurns:       5,
		RemainingRecoveryTurns: 2,
		ExpiresAt:              time.Date(2027, time.February, 3, 4, 5, 6, 0, time.UTC),
	}, caps)
}

func TestCapsStateUnmarshalJSONPrefersCurrentRecoveryFields(t *testing.T) {
	t.Parallel()

	const payload = `{
		"MaxRecoveryTurns": 3,
		"RemainingRecoveryTurns": 1,
		"MaxConsecutiveFailedToolCalls": 9,
		"RemainingConsecutiveFailedToolCalls": 8
	}`
	var caps CapsState
	require.NoError(t, json.Unmarshal([]byte(payload), &caps))
	require.Equal(t, 3, caps.MaxRecoveryTurns)
	require.Equal(t, 1, caps.RemainingRecoveryTurns)
}
