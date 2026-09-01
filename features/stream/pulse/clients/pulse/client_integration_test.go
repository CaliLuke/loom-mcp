package pulse

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	streamopts "github.com/CaliLuke/loom/pulse/streaming/options"
)

const (
	runDockerIntegrationEnv     = "LOOM_MCP_RUN_DOCKER_TESTS"
	requireDockerIntegrationEnv = "LOOM_MCP_REQUIRE_DOCKER_TESTS"
)

func TestDockerIntegrationRequested(t *testing.T) {
	for _, tc := range []struct {
		name     string
		run      string
		required string
		want     bool
	}{
		{name: "unset", want: false},
		{name: "disabled", run: "0", required: "0", want: false},
		{name: "selected", run: "1", want: true},
		{name: "required implies selected", required: "1", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(runDockerIntegrationEnv, tc.run)
			t.Setenv(requireDockerIntegrationEnv, tc.required)
			assert.Equal(t, tc.want, dockerIntegrationRequested())
		})
	}
}

func TestClientRedisStreamRoundTrip(t *testing.T) {
	ctx := context.Background()
	rdb := startRedis(t, ctx)
	client, err := New(Options{
		Redis:            rdb,
		StreamMaxLen:     10,
		OperationTimeout: 2 * time.Second,
		StreamOptions: func(name string) []streamopts.Stream {
			assert.Equal(t, "events", name)
			return nil
		},
	})
	require.NoError(t, err)
	require.NoError(t, client.Close(ctx))

	stream, err := client.Stream("events")
	require.NoError(t, err)
	sink, err := stream.NewSink(
		ctx,
		"audit",
		streamopts.WithSinkBlockDuration(50*time.Millisecond),
		streamopts.WithSinkBufferSize(1),
	)
	require.NoError(t, err)
	events := sink.Subscribe()
	t.Cleanup(func() { sink.Close(context.Background()) })

	id, err := stream.Add(ctx, "update", []byte(`{"status":"ready"}`))
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	select {
	case event := <-events:
		require.NotNil(t, event)
		assert.Equal(t, id, event.ID)
		assert.Equal(t, "update", event.EventName)
		assert.JSONEq(t, `{"status":"ready"}`, string(event.Payload))
		require.NoError(t, sink.Ack(ctx, event))
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for Pulse event")
	}

	require.NoError(t, stream.Destroy(ctx))
	exists, err := rdb.Exists(ctx, "pulse:stream:events").Result()
	require.NoError(t, err)
	assert.Zero(t, exists)
}

func TestClientValidation(t *testing.T) {
	_, err := New(Options{})
	require.EqualError(t, err, "redis client is required")

	client, err := New(Options{Redis: redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})})
	require.NoError(t, err)
	_, err = client.Stream("")
	require.EqualError(t, err, "stream name is required")
}

func startRedis(t *testing.T, ctx context.Context) *redis.Client {
	t.Helper()
	if !dockerIntegrationRequested() {
		t.Skipf("set %s=1 to run Docker-backed Pulse contracts", runDockerIntegrationEnv)
	}
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if os.Getenv(requireDockerIntegrationEnv) == "1" {
			require.NoError(t, err, "required Docker-backed Pulse client contract unavailable")
		}
		t.Skipf("Docker not available: %v", err)
	}
	t.Cleanup(func() {
		require.NoError(t, container.Terminate(context.Background()))
	})
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "6379")
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	require.NoError(t, rdb.Ping(ctx).Err())
	return rdb
}

func dockerIntegrationRequested() bool {
	return os.Getenv(runDockerIntegrationEnv) == "1" || os.Getenv(requireDockerIntegrationEnv) == "1"
}
