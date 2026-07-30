// Command registry runs the internal tool registry gRPC server.
//
// The registry acts as both a catalog and gateway — agents discover toolsets
// through the registry and invoke tools through it.
//
// # Clustering
//
// Multiple nodes with the same REGISTRY_NAME and REDIS_URL form a cluster,
// automatically sharing state and coordinating health checks. Clients can
// connect to any node and see the same registry state.
//
// # Configuration
//
// Environment variables:
//
//	REGISTRY_ADDR          - gRPC listen address (default: ":9090")
//	REGISTRY_NAME          - Registry cluster name (default: "registry")
//	REDIS_URL              - Redis connection URL (default: "localhost:6379")
//	REDIS_PASSWORD         - Redis password (optional)
//	PING_INTERVAL          - Health check ping interval (default: "10s", minimum: "100ms")
//	MISSED_PING_THRESHOLD  - Positive missed-ping count before unhealthy (default: 3)
//
// # Example
//
// Single node:
//
//	REDIS_URL=localhost:6379 go run ./registry/cmd/registry
//
// Multi-node cluster (run on different hosts/ports):
//
//	REGISTRY_NAME=prod REGISTRY_ADDR=:9090 REDIS_URL=redis:6379 ./registry
//	REGISTRY_NAME=prod REGISTRY_ADDR=:9091 REDIS_URL=redis:6379 ./registry
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/registry"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRegistryAddress     = ":9090"
	defaultRegistryName        = "registry"
	defaultRedisURL            = "localhost:6379"
	defaultPingInterval        = 10 * time.Second
	defaultMissedPingThreshold = 3
	minimumPingInterval        = 100 * time.Millisecond
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	// Load configuration from environment.
	addr := envOr("REGISTRY_ADDR", defaultRegistryAddress)
	name := envOr("REGISTRY_NAME", defaultRegistryName)
	redisURL := envOr("REDIS_URL", defaultRedisURL)
	redisPassword := os.Getenv("REDIS_PASSWORD")
	pingInterval, err := envPingInterval()
	if err != nil {
		return err
	}
	missedPingThreshold, err := envMissedPingThreshold()
	if err != nil {
		return err
	}

	// Connect to Redis.
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisURL,
		Password: redisPassword,
	})
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Printf("close redis: %v", err)
		}
	}()

	// Verify Redis connection.
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("connect to redis: %w", err)
	}

	// Create the registry.
	reg, err := registry.New(ctx, registry.Config{
		Redis:               rdb,
		Name:                name,
		PingInterval:        pingInterval,
		MissedPingThreshold: missedPingThreshold,
	})
	if err != nil {
		return fmt.Errorf("create registry: %w", err)
	}

	// Run the registry server.
	log.Printf("starting registry on %s (name=%s)", addr, name)
	if err := reg.Run(ctx, addr); err != nil {
		return fmt.Errorf("run registry: %w", err)
	}

	return nil
}

// envOr returns the environment variable value or a default.
func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// envMissedPingThreshold returns the configured missed-ping threshold.
func envMissedPingThreshold() (int, error) {
	const key = "MISSED_PING_THRESHOLD"
	v := os.Getenv(key)
	if v == "" {
		return defaultMissedPingThreshold, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", key, v, err)
	}
	if i <= 0 {
		return 0, fmt.Errorf("invalid %s value %q: must be greater than zero", key, v)
	}
	return i, nil
}

// envPingInterval returns the configured ping interval.
func envPingInterval() (time.Duration, error) {
	const key = "PING_INTERVAL"
	v := os.Getenv(key)
	if v == "" {
		return defaultPingInterval, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", key, v, err)
	}
	if d < minimumPingInterval {
		return 0, fmt.Errorf("invalid %s value %q: must be at least %s", key, v, minimumPingInterval)
	}
	return d, nil
}
