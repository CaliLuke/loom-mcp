package agenttest

import "os"

// Setenv keeps the parent's process-state safety checks and restores the value
// during attempt cleanup, before the next attempt starts.
func (t *attemptTB) Setenv(key, value string) {
	t.Helper()
	previous, present := os.LookupEnv(key)
	t.TB.Setenv(key, value)
	t.Cleanup(func() {
		t.restoreEnv(key, previous, present)
	})
}

// Chdir preserves testing's PWD behavior and parallel-test checks while restoring
// both the directory and PWD at the end of this attempt.
func (t *attemptTB) Chdir(dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pwd, present := os.LookupEnv("PWD")
	t.TB.Chdir(dir)
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("agenttest: restore attempt directory: %v", err)
		}
		t.restoreEnv("PWD", pwd, present)
	})
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
