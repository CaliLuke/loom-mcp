package registry

import (
	"testing"
	"time"

	"github.com/CaliLuke/go-redis/v9"
	mockpulse "github.com/CaliLuke/loom-mcp/v2/features/stream/pulse/clients/pulse/mocks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceConfigurationRejectsEachMissingDependencyAndInvalidDuration(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*serviceOptions)
	}{
		{"catalog", "catalog", func(o *serviceOptions) {
			o.catalog = nil
		}},
		{"streams", "stream manager", func(o *serviceOptions) {
			o.StreamManager = nil
		}},
		{"health", "health tracker", func(o *serviceOptions) {
			o.HealthTracker = nil
		}},
		{"admissions", "admission store", func(o *serviceOptions) {
			o.CallAdmissions = nil
		}},
		{"pulse", "pulse client", func(o *serviceOptions) {
			o.PulseClient = nil
		}},
		{"negative lease", "negative", func(o *serviceOptions) {
			o.ProviderLeaseDuration = -1
		}},
		{"short execution", "execution timeout", func(o *serviceOptions) {
			o.ExecutionTimeout = time.Nanosecond
		}},
		{"long execution", "execution timeout", func(o *serviceOptions) {
			o.ExecutionTimeout = toolregistry.MaxToolCallWait + time.Millisecond
		}},
		{"short retention", "result stream TTL", func(o *serviceOptions) {
			o.ResultStreamTTL = toolregistry.MinResultStreamTTL - time.Millisecond
		}},
		{"long retention", "result stream TTL", func(o *serviceOptions) {
			o.ResultStreamTTL = toolregistry.MaxResultStreamTTL + time.Millisecond
		}},
		{"short lease", "at least", func(o *serviceOptions) {
			o.ProviderLeaseDuration = toolregistry.MinProviderLeaseDuration - time.Millisecond
		}},
		{"long lease", "must not exceed", func(o *serviceOptions) {
			o.ProviderLeaseDuration = toolregistry.MaxProviderLeaseDuration + time.Millisecond
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := serviceOptions{catalog: newToolsetCatalog(newTestCatalogMap(), newTestTimeSource(time.Unix(1, 0))), StreamManager: &unitStreamManager{}, HealthTracker: &closingHealthTracker{}, CallAdmissions: &recordingCallAdmissions{}, PulseClient: mockpulse.NewClient(t)}
			tc.edit(&opts)
			service, err := newService(opts)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, service)
		})
	}
}

func TestRegistryConfigurationRejectsInvalidDurationsBeforeConnecting(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	require.NoError(t, client.Close())
	for _, tc := range []struct {
		name, want string
		config     Config
	}{
		{"no redis", "redis client", Config{}},
		{"short retention", "result stream TTL", Config{Redis: client, ResultStreamTTL: time.Millisecond}},
		{"long retention", "result stream TTL", Config{Redis: client, ResultStreamTTL: toolregistry.MaxResultStreamTTL + time.Millisecond}},
		{"short execution", "execution timeout", Config{Redis: client, ExecutionTimeout: time.Nanosecond}},
		{"long execution", "execution timeout", Config{Redis: client, ExecutionTimeout: toolregistry.MaxToolCallWait + time.Millisecond}},
		{"short lease", "provider lease", Config{Redis: client, ProviderLeaseDuration: time.Millisecond}},
		{"long lease", "provider lease", Config{Redis: client, ProviderLeaseDuration: toolregistry.MaxProviderLeaseDuration + time.Millisecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := New(t.Context(), tc.config)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, registry)
			assert.NotErrorIs(t, err, redis.ErrClosed, "configuration must fail before accessing the closed client")
		})
	}
}
