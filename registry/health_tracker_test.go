package registry

// These tests pin catalog-owned health epochs independently from Pulse pool
// integration. Redis integration tests cover the same CAS transitions across
// registry replicas.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type healthCaptureLogger struct {
	warnings []string
}

func (*healthCaptureLogger) Debug(context.Context, string, ...any) {}

func (*healthCaptureLogger) Info(context.Context, string, ...any) {}

func (l *healthCaptureLogger) Warn(_ context.Context, message string, _ ...any) {
	l.warnings = append(l.warnings, message)
}

func (*healthCaptureLogger) Error(context.Context, string, ...any) {}

func TestHealthTrackerHelperValidation(t *testing.T) {
	t.Parallel()

	logger := telemetry.NewNoopLogger()
	options := healthTrackerOptions{}
	WithHealthLogger(logger)(&options)
	assert.Equal(t, logger, options.logger)

	validToken := strings.Repeat("a", 64)
	validUUID := uuid.NewString()
	for _, pingID := range []string{
		"malformed",
		"invalid/1/" + validUUID,
		validToken + "/0/" + validUUID,
		validToken + "/1/invalid",
	} {
		_, _, ok := parsePingID(pingID)
		assert.False(t, ok, pingID)
	}
	token, epoch, ok := parsePingID(validToken + "/2/" + validUUID)
	require.True(t, ok)
	assert.Equal(t, validToken, token)
	assert.Equal(t, uint64(2), epoch)

	assert.Empty(t, toolsetFromCatalogKey("unrelated"))
	assert.Equal(t, "weather", toolsetFromCatalogKey(toolsetCatalogKey("weather")))
}

func TestHealthTrackerPongIsMonotonicAndIncarnationFenced(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	clock := newTestTimeSource(now)
	catalog := newToolsetCatalog(newTestCatalogMap(), clock)
	incarnation := uuid.NewString()
	admission, err := catalog.Register(
		ctx,
		testCatalogToolset("test.toolset", "test", nil),
		testAdmissionRevisionA,
		"provider-a",
		incarnation,
		time.Minute,
	)
	require.NoError(t, err)
	tracker := newDirectHealthTracker(ctx, catalog)
	pingID := newPingID(admission.RegistrationToken, admission.HealthEpoch)

	require.NoError(t, tracker.RecordPong(ctx, "test.toolset", "provider-a", incarnation, pingID))
	health, err := tracker.Health(ctx, "test.toolset", admission.RegistrationToken)
	require.NoError(t, err)
	assert.Equal(t, now, health.LastPong)
	assert.True(t, health.Healthy)

	clock.Set(now.Add(-time.Minute))
	require.NoError(t, tracker.RecordPong(ctx, "test.toolset", "provider-a", incarnation, pingID))
	entry, _, err := catalog.healthEntry(ctx, "test.toolset")
	require.NoError(t, err)
	assert.Equal(t, now.UnixNano(), entry.LastPongUnixNano)

	require.NoError(t, catalog.ReleaseProvider(
		ctx,
		"test.toolset",
		"provider-a",
		incarnation,
		admission.RegistrationToken,
	))
	require.NoError(t, tracker.RecordPong(ctx, "test.toolset", "provider-a", incarnation, pingID))
	entry, _, err = catalog.healthEntry(ctx, "test.toolset")
	require.NoError(t, err)
	assert.Zero(t, entry.LastPongUnixNano)
	assert.Equal(t, admission.HealthEpoch+1, entry.HealthEpoch)
}

func TestHealthTrackerZeroLeaseReregistrationRejectsOldPong(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clock := newTestTimeSource(time.Unix(1_700_000_000, 0))
	catalog := newToolsetCatalog(newTestCatalogMap(), clock)
	firstIncarnation := uuid.NewString()
	first, err := catalog.Register(
		ctx,
		testCatalogToolset("test.toolset", "test", nil),
		testAdmissionRevisionA,
		"provider",
		firstIncarnation,
		time.Minute,
	)
	require.NoError(t, err)
	oldPing := newPingID(first.RegistrationToken, first.HealthEpoch)
	require.NoError(t, catalog.ReleaseProvider(
		ctx,
		"test.toolset",
		"provider",
		firstIncarnation,
		first.RegistrationToken,
	))
	secondIncarnation := uuid.NewString()
	second, err := catalog.Register(
		ctx,
		testCatalogToolset("test.toolset", "test", nil),
		testAdmissionRevisionA,
		"provider",
		secondIncarnation,
		time.Minute,
	)
	require.NoError(t, err)
	assert.Greater(t, second.HealthEpoch, first.HealthEpoch)
	tracker := newDirectHealthTracker(ctx, catalog)

	require.NoError(t, tracker.RecordPong(ctx, "test.toolset", "provider", firstIncarnation, oldPing))
	health, err := tracker.Health(ctx, "test.toolset", second.RegistrationToken)
	require.NoError(t, err)
	assert.False(t, health.Healthy)

	require.NoError(t, tracker.RecordPong(
		ctx,
		"test.toolset",
		"provider",
		secondIncarnation,
		newPingID(second.RegistrationToken, second.HealthEpoch),
	))
	health, err = tracker.Health(ctx, "test.toolset", second.RegistrationToken)
	require.NoError(t, err)
	assert.True(t, health.Healthy)
}

func TestHealthTrackerLogsHealthyToStaleTransitionOnce(t *testing.T) {
	t.Parallel()

	lastPong := time.Unix(1_700_000_000, 0)
	logger := &healthCaptureLogger{}
	tracker := &healthTracker{
		logger:              logger,
		lastObservedHealthy: make(map[string]bool),
	}
	healthy := ToolsetHealth{Healthy: true, LastPong: lastPong, ProviderCount: 1}
	stale := ToolsetHealth{
		Healthy:       false,
		LastPong:      lastPong,
		Age:           time.Minute,
		ProviderCount: 1,
	}

	tracker.noteHealth(context.Background(), "test.toolset", healthy)
	tracker.noteHealth(context.Background(), "test.toolset", stale)
	tracker.noteHealth(context.Background(), "test.toolset", stale)

	require.Equal(t, []string{"toolset became unhealthy"}, logger.warnings)
}

func newDirectHealthTracker(_ context.Context, catalog *toolsetCatalog) *healthTracker {
	closed := make(chan struct{})
	close(closed)
	return &healthTracker{
		catalog:             catalog,
		catalogMap:          catalog.m,
		pingInterval:        time.Second,
		missedPingThreshold: 1,
		stalenessThreshold:  2 * time.Second,
		logger:              telemetry.NewNoopLogger(),
		revFloors:           make(map[string]int64),
		lastObservedHealthy: make(map[string]bool),
		closeCh:             make(chan struct{}),
		doneCh:              closed,
	}
}
