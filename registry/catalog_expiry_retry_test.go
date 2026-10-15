package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type conflictingCatalogMap struct {
	*testCatalogMap
	conflicts int
	writes    int
	fail      bool
}

func TestCatalogExpiryPruningRetriesContendedWrites(t *testing.T) {
	for _, operation := range []string{"drain", "release", "pong", "health"} {
		for _, fail := range []bool{false, true} {
			name := operation + "/retry"
			if fail {
				name = operation + "/storage failure"
			}
			t.Run(name, func(t *testing.T) {
				now := time.Unix(1700000000, 0)
				entry := testPersistedCatalogEntry(t, "toolset", now)
				entry.ProviderLeases[providerLeaseKey("provider", testIncarnationA)] = providerLease{ExpiresAtUnixMilli: now.UnixMilli()}
				body, err := json.Marshal(entry)
				require.NoError(t, err)
				storage := &conflictingCatalogMap{testCatalogMap: newTestCatalogMap(), conflicts: 1, fail: fail}
				storage.content[toolsetCatalogKey("toolset")] = string(body)
				catalog := newToolsetCatalog(storage, newTestTimeSource(now))
				switch operation {
				case "drain":
					err = catalog.DrainProvider(t.Context(), "toolset", "provider", testIncarnationA, entry.RegistrationToken, time.Minute)
				case "release":
					err = catalog.ReleaseProvider(t.Context(), "toolset", "provider", testIncarnationA, entry.RegistrationToken)
				case "pong":
					err = catalog.RecordPong(t.Context(), "toolset", "provider", testIncarnationA, entry.RegistrationToken, entry.HealthEpoch)
				case "health":
					_, _, err = catalog.HealthIdentity(t.Context(), "toolset")
				}
				if fail {
					require.ErrorIs(t, err, assert.AnError)
					assert.Equal(t, string(body), storage.content[toolsetCatalogKey("toolset")])
					return
				}
				if operation == "health" {
					require.ErrorIs(t, err, errToolsetNotFound)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, 2, storage.writes, "lost CAS must retry pruning")
				active, err := catalog.ActiveRegistration(t.Context(), "toolset")
				require.NoError(t, err)
				assert.Empty(t, active.ProviderLeases)
				assert.Equal(t, entry.HealthEpoch+1, active.HealthEpoch)
				assert.Zero(t, active.LastPongUnixNano)
			})
		}
	}
}

func (m *conflictingCatalogMap) TestAndSetEx(ctx context.Context, key, test, value string) (string, bool, bool, error) {
	if test != "" {
		m.writes++
		if m.fail {
			return "", false, false, assert.AnError
		}
		if m.conflicts > 0 {
			m.conflicts--
			return test, true, false, nil
		}
	}
	return m.testCatalogMap.TestAndSetEx(ctx, key, test, value)
}
