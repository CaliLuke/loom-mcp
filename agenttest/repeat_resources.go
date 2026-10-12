package agenttest

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

// Setenv retains testing's parallel restrictions but reports OS failures to the
// attempt and restores the variable before the next attempt starts.
func (t *attemptTB) Setenv(key, value string) {
	t.Helper()
	t.checkProcessState()
	previous, present := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		t.restoreEnv(key, previous, present)
	})
}

// Chdir owns its directory handle and PWD restoration so failures and cleanup
// belong to the attempt while testing still enforces parallel restrictions.
func (t *attemptTB) Chdir(dir string) {
	t.Helper()
	t.checkProcessState()
	previous, err := os.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(errors.Join(err, previous.Close()))
	}
	t.Cleanup(func() {
		if err := errors.Join(previous.Chdir(), previous.Close()); err != nil {
			t.Errorf("agenttest: restore attempt directory: %v", err)
		}
	})
	switch runtime.GOOS {
	case "windows", "plan9":
	default:
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PWD", cwd)
	}
}

// TempDir creates a directory owned and removed by this attempt's cleanup.
func (t *attemptTB) TempDir() string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agenttest-attempt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("agenttest: remove attempt temporary directory: %v", err)
		}
	})
	return dir
}

// ArtifactDir lazily creates a stable directory beneath the parent's artifacts.
// The parent owns its lifetime, so attempt cleanup preserves diagnostic files.
func (t *attemptTB) ArtifactDir() string {
	t.Helper()
	t.artifactOnce.Do(func() {
		t.artifactDir, t.artifactErr = os.MkdirTemp(t.artifactBase, fmt.Sprintf("attempt-%d-", t.index))
	})
	if t.artifactErr != nil {
		t.Fatalf("agenttest: ArtifactDir: %v", t.artifactErr)
	}
	return t.artifactDir
}

// checkProcessState uses a valid, unchanged environment value to invoke the
// parent's parallel/fuzz safety checks before any attempt-owned OS mutation.
func (t *attemptTB) checkProcessState() {
	t.processState.Do(func() {
		pwd, present := os.LookupEnv("PWD")
		t.TB.Setenv("PWD", pwd)
		if !present {
			if err := os.Unsetenv("PWD"); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func (t *attemptTB) restoreEnv(key, value string, present bool) {
	var err error
	if present {
		err = os.Setenv(key, value)
	} else {
		err = os.Unsetenv(key)
	}
	if err != nil {
		t.Errorf("agenttest: restore attempt environment: %v", err)
	}
}
