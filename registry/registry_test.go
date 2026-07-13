package registry

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/CaliLuke/loom/pulse/pool"
	"github.com/stretchr/testify/require"
)

func TestShutdownSignalsRestoreDefaultDisposition(t *testing.T) {
	const helperEnv = "LOOM_MCP_SIGNAL_STOP_HELPER"
	if os.Getenv(helperEnv) == "1" {
		_, stop := shutdownSignals()
		stop()
		process, err := os.FindProcess(os.Getpid())
		if err != nil {
			os.Exit(2)
		}
		if err := process.Signal(syscall.SIGTERM); err != nil {
			os.Exit(3)
		}
		time.Sleep(time.Second)
		os.Exit(4)
	}

	// Race-enabled full-suite builds can delay helper process scheduling for
	// several seconds. The helper still exits with status 4 after one second if
	// SIGTERM is not restored, so this deadline only bounds startup failures.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShutdownSignalsRestoreDefaultDisposition$") // #nosec G204,G702 -- fixed test binary and test name
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper error = %v, want signal exit", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("helper status = %v, want SIGTERM", exitErr.Sys())
	}
}

// TestNewRegistry verifies that the Registry constructor wires all components correctly.
func TestNewRegistry(t *testing.T) {
	rdb := getRedis(t)
	ctx := context.Background()

	// Create registry with default config.
	reg, err := New(ctx, Config{
		Redis:               rdb,
		Name:                "test-" + t.Name(),
		PingInterval:        50 * time.Millisecond,
		MissedPingThreshold: 2,
		PoolNodeOptions:     testNodeOpts(),
	})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}
	defer func() {
		if err := reg.Close(ctx); err != nil {
			t.Errorf("failed to close registry: %v", err)
		}
	}()

	// Verify service is accessible.
	if reg.Service() == nil {
		t.Error("Service() should return non-nil service")
	}
}

// TestNewRegistryRequiresRedis verifies that Redis client is required.
func TestNewRegistryRequiresRedis(t *testing.T) {
	ctx := context.Background()

	_, err := New(ctx, Config{})
	if err == nil {
		t.Error("expected error when Redis is nil")
	}
}

func TestNewRegistryRejectsNegativeHealthConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{name: "ping interval", config: Config{PingInterval: -time.Second}, want: "ping interval must not be negative"},
		{name: "missed ping threshold", config: Config{MissedPingThreshold: -1}, want: "missed ping threshold must not be negative"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(context.Background(), test.config)
			if err == nil || err.Error() != test.want {
				t.Fatalf("New() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateConfigAllowsZeroHealthDefaults(t *testing.T) {
	require.NoError(t, validateConfig(Config{}))
}

// TestRegistryGracefulShutdown verifies that Close properly cleans up resources.
func TestRegistryGracefulShutdown(t *testing.T) {
	rdb := getRedis(t)
	ctx := context.Background()

	reg, err := New(ctx, Config{
		Redis:               rdb,
		Name:                "test-" + t.Name(),
		PingInterval:        50 * time.Millisecond,
		MissedPingThreshold: 2,
		PoolNodeOptions: []pool.NodeOption{
			pool.WithJobSinkBlockDuration(100 * time.Millisecond),
		},
	})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	// Close should complete without error.
	if err := reg.Close(ctx); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	// Calling Close again should be safe (idempotent health tracker close).
	// Note: Other components may error on double-close, but that's expected.
}

// TestRunCleansUpAfterContextCancel verifies that Run performs shutdown
// cleanup with a live context after its run context is canceled.
//
// Regression: Run passed its own already-canceled context to Close, so pool
// node cleanup (job requeue, node stream destroy) silently failed inside loom
// and stale Pulse state was left behind in Redis.
func TestRunCleansUpAfterContextCancel(t *testing.T) {
	rdb := getRedis(t)
	ctx := context.Background()

	reg, err := New(ctx, Config{
		Redis:               rdb,
		Name:                "run-test-" + t.Name(),
		PingInterval:        50 * time.Millisecond,
		MissedPingThreshold: 2,
		PoolNodeOptions:     testNodeOpts(),
	})
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	runErr := make(chan error, 1)
	go func() {
		runErr <- reg.Run(runCtx, "127.0.0.1:0")
	}()

	// Wait until the pool node owns a real Pulse stream, then trigger the
	// documented ctx-cancel shutdown path. This proves cleanup after startup
	// without relying on a scheduler delay.
	require.Eventually(t, func() bool {
		keys, keysErr := rdb.Keys(ctx, "pulse:stream:*:node:*").Result()
		return keysErr == nil && len(keys) > 0
	}, 10*time.Second, 10*time.Millisecond)
	cancelRun()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on canceled-context shutdown: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	// The pool node stream must be destroyed during shutdown; a canceled
	// cleanup context leaves it behind in Redis.
	keys, err := rdb.Keys(ctx, "pulse:stream:*:node:*").Result()
	if err != nil {
		t.Fatalf("failed to list node stream keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected node stream cleanup during Run shutdown, found stale keys: %v", keys)
	}
}
