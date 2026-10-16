package registry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingCatalogMap struct {
	*testCatalogMap
	readErr, writeErr, keysErr error
}

type catalogOperation struct {
	name string
	call func(context.Context, *toolsetCatalog, catalogEntry) error
}

func TestCatalogOperationsDoNotHideStorageOrCorruptionFailures(t *testing.T) {
	for _, op := range catalogFailureOperations(t) {
		for _, fault := range []string{"read", "corrupt"} {
			t.Run(op.name+"/"+fault, func(t *testing.T) {
				now := time.Unix(1700000000, 0)
				entry := testPersistedCatalogEntry(t, "toolset", now)
				body, err := json.Marshal(entry)
				require.NoError(t, err)
				storage := &failingCatalogMap{testCatalogMap: newTestCatalogMap()}
				storage.content[toolsetCatalogKey("toolset")] = string(body)
				if fault == "read" {
					storage.readErr = assert.AnError
				} else {
					storage.content[toolsetCatalogKey("toolset")] += " {}"
				}
				before := storage.content[toolsetCatalogKey("toolset")]
				catalog := newToolsetCatalog(storage, newTestTimeSource(now))
				err = op.call(t.Context(), catalog, entry)
				require.Error(t, err)
				if fault == "read" {
					require.ErrorIs(t, err, assert.AnError)
				} else {
					require.ErrorContains(t, err, "trailing JSON")
				}
				assert.Equal(t, before, storage.content[toolsetCatalogKey("toolset")], "failed reads must not change admission ownership")
			})
		}
	}
}

func TestCatalogDiscoveryRejectsInvalidPersistedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*catalogEntry)
		want string
	}{
		{"missing toolset", func(e *catalogEntry) {
			e.Toolset = nil
		}, "toolset payload"},
		{"wrong toolset", func(e *catalogEntry) {
			e.Toolset.Name = "other"
		}, "toolset payload"},
		{"missing date", func(e *catalogEntry) {
			e.RegisteredAt = ""
		}, "registered_at"},
		{"bad date", func(e *catalogEntry) {
			e.RegisteredAt = "invalid"
			e.Toolset.RegisteredAt = "invalid"
		}, "registered_at"},
		{"revision", func(e *catalogEntry) {
			e.AdmissionRevision = "!invalid"
		}, "admission revision"},
		{"schema changed", func(e *catalogEntry) {
			e.SchemaFingerprint = "invalid"
		}, "schema fingerprint"},
		{"token changed", func(e *catalogEntry) {
			e.RegistrationToken = testStaleToken
		}, "registration token"},
		{"state", func(e *catalogEntry) {
			e.State = "unknown"
		}, "catalog state"},
		{"missing leases", func(e *catalogEntry) {
			e.ProviderLeases = nil
		}, "lease map"},
		{"epoch", func(e *catalogEntry) {
			e.HealthEpoch = 0
		}, "health epoch"},
		{"pong", func(e *catalogEntry) {
			e.LastPongUnixNano = -1
		}, "pong timestamp"},
		{"lease key", func(e *catalogEntry) {
			e.ProviderLeases = map[string]providerLease{"bad": {ExpiresAtUnixMilli: 1}}
		}, "lease key"},
		{"lease incarnation", func(e *catalogEntry) {
			e.ProviderLeases = map[string]providerLease{"provider\x00bad": {ExpiresAtUnixMilli: 1}}
		}, "lease key"},
		{"lease deadline", func(e *catalogEntry) {
			e.ProviderLeases = map[string]providerLease{providerLeaseKey("provider", testIncarnationA): {}}
		}, "lease deadline"},
		{"missing tombstones", func(e *catalogEntry) {
			e.RetiredTokens = nil
		}, "retired registration token set"},
		{"bad tombstone", func(e *catalogEntry) {
			e.RetiredTokens = map[string]struct{}{"bad": {}}
		}, "retired registration token"},
		{"retired active token", func(e *catalogEntry) {
			e.RetiredTokens[e.RegistrationToken] = struct{}{}
		}, "active token is retired"},
		{"missing retired token", func(e *catalogEntry) {
			e.State = catalogEntryRetired
		}, "missing its tombstone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1700000000, 0)
			entry := testPersistedCatalogEntry(t, "toolset", now)
			tc.edit(&entry)
			body, err := json.Marshal(entry)
			require.NoError(t, err)
			storage := newTestCatalogMap()
			storage.content[toolsetCatalogKey("toolset")] = string(body)
			catalog := newToolsetCatalog(storage, newTestTimeSource(now))
			_, err = catalog.GetToolset(t.Context(), "toolset")
			require.ErrorContains(t, err, tc.want)
			assert.Equal(t, string(body), storage.content[toolsetCatalogKey("toolset")])
		})
	}
}

func TestCatalogDiscoveryPropagatesEnumerationFailures(t *testing.T) {
	catalog := newToolsetCatalog(&failingCatalogMap{testCatalogMap: newTestCatalogMap(), keysErr: assert.AnError}, newTestTimeSource(time.Unix(1, 0)))
	_, err := catalog.ListToolsets(t.Context(), nil)
	require.ErrorIs(t, err, assert.AnError)
	_, err = catalog.SearchToolsets(t.Context(), "toolset")
	require.ErrorIs(t, err, assert.AnError)
}

func TestCatalogMutationsPreserveOwnershipWhenCommitFails(t *testing.T) {
	for _, op := range catalogFailureOperations(t) {
		if op.name != "register" && op.name != "drain" && op.name != "release" && op.name != "retire" && op.name != "pong" {
			continue
		}
		for _, fault := range []string{"write", "clock"} {
			if fault == "clock" && (op.name == "retire" || op.name == "release") {
				continue
			}
			t.Run(op.name+"/"+fault, func(t *testing.T) {
				now := time.Unix(1700000000, 0)
				entry := testPersistedCatalogEntry(t, "toolset", now)
				body, err := json.Marshal(entry)
				require.NoError(t, err)
				storage := &failingCatalogMap{testCatalogMap: newTestCatalogMap()}
				storage.content[toolsetCatalogKey("toolset")] = string(body)
				clock := newTestTimeSource(now.Add(time.Second))
				if fault == "clock" {
					clock.err = assert.AnError
				} else {
					storage.writeErr = assert.AnError
				}
				err = op.call(t.Context(), newToolsetCatalog(storage, clock), entry)
				require.ErrorIs(t, err, assert.AnError)
				assert.Equal(t, string(body), storage.content[toolsetCatalogKey("toolset")])
			})
		}
	}
}

func (m *failingCatalogMap) TestAndSetEx(ctx context.Context, key, test, value string) (string, bool, bool, error) {
	if test == "" && value == "" && m.readErr != nil {
		return "", false, false, m.readErr
	}
	if test != "" && m.writeErr != nil {
		return "", false, false, m.writeErr
	}
	return m.testCatalogMap.TestAndSetEx(ctx, key, test, value)
}

func (m *failingCatalogMap) AuthoritativeKeys(ctx context.Context) ([]string, error) {
	if m.keysErr != nil {
		return nil, m.keysErr
	}
	return m.testCatalogMap.AuthoritativeKeys(ctx)
}

func catalogFailureOperations(t *testing.T) []catalogOperation {
	return []catalogOperation{
		{"register", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			_, err := c.Register(ctx, e.Toolset, e.AdmissionRevision, "provider", testIncarnationA, time.Minute)
			return err
		}},
		{"drain", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			return c.DrainProvider(ctx, "toolset", "provider", testIncarnationA, e.RegistrationToken, time.Minute)
		}},
		{"release", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			return c.ReleaseProvider(ctx, "toolset", "provider", testIncarnationA, e.RegistrationToken)
		}},
		{"retire", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			return c.Retire(ctx, "toolset", e.RegistrationToken)
		}},
		{"get", func(ctx context.Context, c *toolsetCatalog, _ catalogEntry) error {
			_, err := c.GetToolset(ctx, "toolset")
			return err
		}},
		{"token", func(ctx context.Context, c *toolsetCatalog, _ catalogEntry) error {
			_, err := c.RegistrationToken(ctx, "toolset")
			return err
		}},
		{"verify", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			return c.VerifyActiveToken(ctx, "toolset", e.RegistrationToken)
		}},
		{"lease", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			active, observedAt, err := c.ActiveProviderLease(ctx, "toolset", "provider", testIncarnationA, e.RegistrationToken)
			assert.False(t, active)
			assert.Zero(t, observedAt, "failed lease lookup must not publish a timestamp")
			return err
		}},
		{"leases", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			_, err := c.ActiveProviderLeases(ctx, "toolset", e.RegistrationToken)
			return err
		}},
		{"health", func(ctx context.Context, c *toolsetCatalog, _ catalogEntry) error {
			_, _, err := c.HealthIdentity(ctx, "toolset")
			return err
		}},
		{"pong", func(ctx context.Context, c *toolsetCatalog, e catalogEntry) error {
			return c.RecordPong(ctx, "toolset", "provider", testIncarnationA, e.RegistrationToken, e.HealthEpoch)
		}},
		{"list", func(ctx context.Context, c *toolsetCatalog, _ catalogEntry) error {
			_, err := c.ListToolsets(ctx, nil)
			return err
		}},
		{"search", func(ctx context.Context, c *toolsetCatalog, _ catalogEntry) error {
			_, err := c.SearchToolsets(ctx, "toolset")
			return err
		}},
	}
}
