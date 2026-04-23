package registry

import (
	"context"
	"testing"
	"time"
)

const (
	testPollInterval = 10 * time.Millisecond
	testWaitTimeout  = 500 * time.Millisecond
)

func waitForCondition(t *testing.T, check func() bool, message string) {
	t.Helper()

	if check() {
		return
	}

	ticker := time.NewTicker(testPollInterval)
	defer ticker.Stop()
	timeout := time.NewTimer(testWaitTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-ticker.C:
			if check() {
				return
			}
		case <-timeout.C:
			t.Fatal(message)
		}
	}
}

func waitForCacheEntry(
	t *testing.T,
	cache Cache,
	key string,
	wantPresent bool,
) *ToolsetSchema {
	t.Helper()

	var schema *ToolsetSchema
	waitForCondition(t, func() bool {
		got, err := cache.Get(context.Background(), key)
		if err != nil {
			return false
		}
		schema = got
		return (got != nil) == wantPresent
	}, "timed out waiting for cache state")
	return schema
}
