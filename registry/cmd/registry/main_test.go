package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvMissedPingThreshold(t *testing.T) {
	t.Run("default when absent", func(t *testing.T) {
		t.Setenv("MISSED_PING_THRESHOLD", "")

		got, err := envMissedPingThreshold()

		require.NoError(t, err)
		assert.Equal(t, defaultMissedPingThreshold, got)
	})

	t.Run("parsed value", func(t *testing.T) {
		t.Setenv("MISSED_PING_THRESHOLD", "7")

		got, err := envMissedPingThreshold()

		require.NoError(t, err)
		assert.Equal(t, 7, got)
	})

	t.Run("invalid value", func(t *testing.T) {
		t.Setenv("MISSED_PING_THRESHOLD", "nope")

		got, err := envMissedPingThreshold()

		require.Error(t, err)
		assert.Equal(t, 0, got)
		assert.Contains(t, err.Error(), `invalid MISSED_PING_THRESHOLD value "nope"`)
	})

	for _, value := range []string{"0", "-2"} {
		t.Run("non-positive "+value, func(t *testing.T) {
			t.Setenv("MISSED_PING_THRESHOLD", value)

			got, err := envMissedPingThreshold()

			require.Error(t, err)
			assert.Equal(t, 0, got)
			assert.Contains(t, err.Error(), "must be greater than zero")
		})
	}
}

func TestEnvPingInterval(t *testing.T) {
	t.Run("default when absent", func(t *testing.T) {
		t.Setenv("PING_INTERVAL", "")

		got, err := envPingInterval()

		require.NoError(t, err)
		assert.Equal(t, defaultPingInterval, got)
	})

	t.Run("parsed value", func(t *testing.T) {
		t.Setenv("PING_INTERVAL", "250ms")

		got, err := envPingInterval()

		require.NoError(t, err)
		assert.Equal(t, 250*time.Millisecond, got)
	})

	t.Run("invalid value", func(t *testing.T) {
		t.Setenv("PING_INTERVAL", "soon")

		got, err := envPingInterval()

		require.Error(t, err)
		assert.Equal(t, time.Duration(0), got)
		assert.Contains(t, err.Error(), `invalid PING_INTERVAL value "soon"`)
	})

	for _, value := range []string{"0s", "-10s", "1ns", "99ms"} {
		t.Run("out of range "+value, func(t *testing.T) {
			t.Setenv("PING_INTERVAL", value)

			got, err := envPingInterval()

			require.Error(t, err)
			assert.Equal(t, time.Duration(0), got)
			assert.Contains(t, err.Error(), "must be at least 100ms")
		})
	}
}

func TestRunFailsFastOnInvalidEnv(t *testing.T) {
	t.Run("invalid ping interval", func(t *testing.T) {
		t.Setenv("PING_INTERVAL", "soon")

		err := run()

		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid PING_INTERVAL value "soon"`)
		assert.NotContains(t, err.Error(), "connect to redis")
	})

	t.Run("invalid missed ping threshold", func(t *testing.T) {
		t.Setenv("PING_INTERVAL", "10s")
		t.Setenv("MISSED_PING_THRESHOLD", "many")

		err := run()

		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid MISSED_PING_THRESHOLD value "many"`)
		assert.NotContains(t, err.Error(), "connect to redis")
	})

	t.Run("too small ping interval", func(t *testing.T) {
		t.Setenv("PING_INTERVAL", "1ns")

		err := run()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be at least 100ms")
		assert.NotContains(t, err.Error(), "connect to redis")
	})

	t.Run("non-positive missed ping threshold", func(t *testing.T) {
		t.Setenv("PING_INTERVAL", "10s")
		t.Setenv("MISSED_PING_THRESHOLD", "0")

		err := run()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be greater than zero")
		assert.NotContains(t, err.Error(), "connect to redis")
	})
}
