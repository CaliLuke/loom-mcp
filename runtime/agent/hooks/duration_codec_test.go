package hooks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHookCodecRoundTripsDurationFieldsAsNanoseconds(t *testing.T) {
	type durationPayload struct {
		TimeBudget time.Duration
		Timeouts   map[string]time.Duration
	}

	want := durationPayload{
		TimeBudget: 2 * time.Minute,
		Timeouts:   map[string]time.Duration{"catalog.search": 5 * time.Second},
	}
	payload, err := marshalHookPayload("duration", want)
	require.NoError(t, err)
	require.JSONEq(t, `{"TimeBudget":120000000000,"Timeouts":{"catalog.search":5000000000}}`, string(payload))

	var got durationPayload
	err = decodeHookPayload(&ActivityInput{Type: RunStarted, Payload: payload}, &got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestHookCodecRoundTripsDurationMapKeys(t *testing.T) {
	type durationKeyPayload struct {
		Timeouts map[time.Duration]string
	}

	want := durationKeyPayload{Timeouts: map[time.Duration]string{
		time.Second:     "short",
		2 * time.Second: "long",
	}}
	payload, err := marshalHookPayload("duration keys", want)
	require.NoError(t, err)
	require.JSONEq(t, `{"Timeouts":{"1000000000":"short","2000000000":"long"}}`, string(payload))

	var got durationKeyPayload
	err = decodeHookPayload(&ActivityInput{Type: RunStarted, Payload: payload}, &got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestHookCodecRespectsStringDurationFormat(t *testing.T) {
	type stringDurationPayload struct {
		Timeout time.Duration `json:"timeout,string"`
	}

	want := stringDurationPayload{Timeout: 5 * time.Second}
	payload, err := marshalHookPayload("string duration", want)
	require.NoError(t, err)
	require.JSONEq(t, `{"timeout":"5000000000"}`, string(payload))

	var got stringDurationPayload
	err = decodeHookPayload(&ActivityInput{Type: RunStarted, Payload: payload}, &got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
