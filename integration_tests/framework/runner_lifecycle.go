package framework

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"syscall"
)

const lifecyclePollInterval = 200 * time.Millisecond

// startServer starts the test server.
func (r *Runner) startServer(t *testing.T) error {
	t.Helper()

	if external := os.Getenv("TEST_SERVER_URL"); external != "" {
		return r.configureExternalServer(external)
	}
	return r.startManagedServer(t)
}

// stopServer stops the test server.
func (r *Runner) stopServer() {
	if r.externalServer {
		return
	}
	if r.server == nil || r.server.Process == nil {
		return
	}
	r.stopProcess(syscall.SIGINT, 2*time.Second)
	r.stopProcess(syscall.SIGTERM, time.Second)
	_ = r.server.Process.Kill()
	if r.exitCh != nil {
		select {
		case <-r.exitCh:
		case <-time.After(time.Second):
		}
	}
}

// ping checks if the server is ready.
func (r *Runner) ping() error {
	// Send a minimal invalid JSON-RPC request that does not initialize state.
	body := []byte(`{"jsonrpc":"2.0","id":1}`)
	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		r.baseURL.String()+"/rpc",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	// #nosec G704 -- test runner issues requests to localhost (or a validated TEST_SERVER_URL)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (r *Runner) configureExternalServer(external string) error {
	u, err := url.Parse(external)
	if err != nil {
		return fmt.Errorf("parse TEST_SERVER_URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid TEST_SERVER_URL %q: must include scheme and host", external)
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	r.baseURL = u
	r.externalServer = true
	return nil
}

func (r *Runner) startManagedServer(t *testing.T) error {
	port, err := getFreePort()
	if err != nil {
		return err
	}
	r.baseURL, err = url.Parse("http://localhost:" + port)
	if err != nil {
		return fmt.Errorf("parse local server URL: %w", err)
	}
	workingRoot, err := r.prepareWorkingRoot(t)
	if err != nil {
		return err
	}
	binPath, err := buildServerBinary(workingRoot)
	if err != nil {
		return err
	}
	// Start HTTP server from pre-compiled binary (much faster than go run).
	//nolint:gosec // launching pre-compiled test server binary
	cmd := exec.CommandContext(context.Background(), binPath, "-http-port", port)
	cmd.Env = os.Environ()
	r.stdoutTail = &ringBuffer{max: tailMaxBytes}
	r.stderrTail = &ringBuffer{max: tailMaxBytes}
	cmd.Stdout = r.stdoutTail
	cmd.Stderr = r.stderrTail
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	r.server = cmd
	r.exitCh = make(chan error, 1)
	go func() {
		r.exitCh <- cmd.Wait()
	}()
	return r.waitForServerReady()
}

func (r *Runner) prepareWorkingRoot(t *testing.T) (string, error) {
	exampleRoot := findExampleRoot()
	if exampleRoot == "" {
		return "", fmt.Errorf("could not locate example root")
	}
	workingRoot := exampleRoot
	if r.skipGeneration {
		codegenMu.Lock()
		clonedRoot, err := cloneExampleRoot(exampleRoot)
		codegenMu.Unlock()
		if err != nil {
			return "", err
		}
		if err := applySDKServerFixturePatch(clonedRoot); err != nil {
			_ = os.RemoveAll(clonedRoot)
			return "", err
		}
		t.Cleanup(func() { _ = os.RemoveAll(clonedRoot) })
		return clonedRoot, nil
	}
	// Never mutate the shared fixture root in place. Full-repo test runs execute
	// package test binaries in parallel, so generated examples must be prepared in
	// isolated temp clones.
	if strings.EqualFold(os.Getenv("TEST_SKIP_GENERATION"), "true") {
		return workingRoot, nil
	}
	codegenMu.Lock()
	clonedRoot, err := cloneExampleRoot(exampleRoot)
	codegenMu.Unlock()
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { _ = os.RemoveAll(clonedRoot) })
	if err := regenerateExample(t, clonedRoot); err != nil {
		return "", err
	}
	if err := restoreFixtureCommandTree(exampleRoot, clonedRoot); err != nil {
		_ = os.RemoveAll(clonedRoot)
		return "", err
	}
	return clonedRoot, nil
}

func (r *Runner) waitForServerReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), r.readyTimeout())
	defer cancel()

	err := r.pollLifecycle(ctx, func() (bool, error) {
		select {
		case err := <-r.exitCh:
			return false, r.serverExitedEarly(err)
		default:
		}
		if err := r.ping(); err == nil {
			return true, nil
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	return r.serverReadyTimeout()
}

func (r *Runner) readyTimeout() time.Duration {
	timeout := 30 * time.Second
	if raw := os.Getenv("MCP_TEST_READY_TIMEOUT_SECONDS"); raw != "" {
		if sec, err := strconv.Atoi(raw); err == nil && sec > 0 {
			timeout = time.Duration(sec) * time.Second
		}
	}
	return timeout
}

func (r *Runner) serverExitedEarly(err error) error {
	return fmt.Errorf(
		"server exited early: %w\n-- stdout (tail) --\n%s\n-- stderr (tail) --\n%s",
		err,
		string(r.stdoutTail.Bytes()),
		string(r.stderrTail.Bytes()),
	)
}

func (r *Runner) serverReadyTimeout() error {
	return fmt.Errorf(
		"server failed to become ready at %s\n-- stdout (tail) --\n%s\n-- stderr (tail) --\n%s",
		r.baseURL,
		string(r.stdoutTail.Bytes()),
		string(r.stderrTail.Bytes()),
	)
}

func (r *Runner) stopProcess(sig syscall.Signal, wait time.Duration) {
	_ = r.server.Process.Signal(sig)
	if r.exitCh == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_ = r.pollLifecycle(ctx, func() (bool, error) {
		select {
		case <-r.exitCh:
			return true, nil
		default:
			return false, nil
		}
	})
}

func (r *Runner) pollLifecycle(ctx context.Context, check func() (bool, error)) error {
	ready, err := check()
	if ready || err != nil {
		return err
	}

	ticker := time.NewTicker(lifecyclePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			ready, err := check()
			if ready || err != nil {
				return err
			}
		}
	}
}
