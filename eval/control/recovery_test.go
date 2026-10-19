package control_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/control/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlRecoveryCrashBoundaries(t *testing.T) {
	cases := []struct {
		point   testfixture.CrashPoint
		state   control.JobState
		effects int
	}{
		{testfixture.BeforeAcceptance, control.Queued, 0},
		{testfixture.AfterAcceptance, control.Queued, 0},
		{testfixture.AfterClaim, control.Interrupted, 0},
		{testfixture.AfterEffect, control.Interrupted, 1},
		{testfixture.BeforeCompletion, control.Interrupted, 1},
		{testfixture.AfterCompletion, control.Succeeded, 1},
	}
	for _, tc := range cases {
		t.Run(string(tc.point), func(t *testing.T) {
			dir := t.TempDir()
			executable, err := os.Executable()
			require.NoError(t, err)
			require.NoError(t, os.Symlink(executable, filepath.Join(dir, "control-crash-child")))
			cmd := exec.CommandContext(t.Context(), "./control-crash-child", "-test.run=^TestControlRecoveryChild$", "-test.count=1")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "CONTROL_CRASH_DIR="+dir, "CONTROL_CRASH_POINT="+string(tc.point))
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, string(output))
			require.Equal(t, 71, exit.ExitCode(), string(output))
			records, err := filepath.Glob(filepath.Join(dir, "*.json"))
			require.NoError(t, err)
			if tc.point == testfixture.BeforeAcceptance {
				assert.Empty(t, records)
			} else {
				assert.Len(t, records, 1)
			}
			e := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
				appendRecoveryEffect(t, dir)
				return finishedRecoveryResult(), nil
			})
			job, err := e.Accept(t.Context(), recoverySubmission())
			require.NoError(t, err)
			assert.Equal(t, tc.state, job.State)
			if tc.point != testfixture.BeforeAcceptance && tc.point != testfixture.AfterAcceptance {
				assert.Contains(t, string(output), "accepted:"+job.ID+"\n")
			}
			assert.Equal(t, tc.effects, recoveryEffects(t, dir))
			if tc.state == control.Queued {
				job, err = e.RunQueued(t.Context(), "scope", job.ID)
				require.NoError(t, err)
				assert.Equal(t, control.Succeeded, job.State)
				assert.Equal(t, 1, recoveryEffects(t, dir))
			} else {
				again, runErr := e.RunQueued(t.Context(), "scope", job.ID)
				require.NoError(t, runErr)
				assert.Equal(t, job, again)
				assert.Equal(t, tc.effects, recoveryEffects(t, dir))
			}
			retry, err := e.Accept(t.Context(), recoverySubmission())
			require.NoError(t, err)
			assert.Equal(t, job, retry)
			if tc.state == control.Succeeded {
				assert.Equal(t, finishedRecoveryResult().Manifest, job.Manifest)
			}
			require.NoError(t, e.Close())
			reopened := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
				t.Fatal("terminal replay")
				return testfixture.Result{}, nil
			})
			persisted, err := reopened.Status(t.Context(), "scope", job.ID)
			require.NoError(t, err)
			assert.Equal(t, job, persisted)
		})
	}
}

func TestControlRecoveryChild(t *testing.T) {
	dir := os.Getenv("CONTROL_CRASH_DIR")
	if dir == "" {
		return
	}
	point := testfixture.CrashPoint(os.Getenv("CONTROL_CRASH_POINT"))
	crash := func(at testfixture.CrashPoint) {
		if at == point {
			os.Exit(71)
		}
	}
	checkpoint := func(at testfixture.CrashPoint) error {
		crash(at)
		return t.Context().Err()
	}
	e, err := testfixture.OpenExecutor(testfixture.ExecutorConfig{Dir: dir, Checkpoint: checkpoint, Run: func(context.Context, control.Submission) (testfixture.Result, error) {
		appendRecoveryEffect(t, dir)
		require.NoError(t, checkpoint(testfixture.AfterEffect))
		return finishedRecoveryResult(), nil
	}})
	require.NoError(t, err)
	job, err := e.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	_, err = fmt.Fprintln(os.Stdout, "accepted:"+job.ID)
	require.NoError(t, err)
	_, err = e.RunQueued(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	require.NoError(t, e.Close())
	t.Fatal("crash checkpoint was not reached")
}

func TestControlRecoveryScopedIdentity(t *testing.T) {
	dir := t.TempDir()
	e := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		return finishedRecoveryResult(), nil
	})
	var wg sync.WaitGroup
	jobs := make(chan control.JobRecord, 12)
	for range 12 {
		wg.Go(func() {
			job, err := e.Accept(t.Context(), recoverySubmission())
			assert.NoError(t, err)
			jobs <- job
		})
	}
	wg.Wait()
	close(jobs)
	var first control.JobRecord
	for job := range jobs {
		if first.ID == "" {
			first = job
		}
		assert.Equal(t, first, job)
	}
	omitted := recoverySubmission()
	omitted.Kind = ""
	same, err := e.Accept(t.Context(), omitted)
	require.NoError(t, err)
	assert.Equal(t, first, same)
	variants := []func(*control.Submission){
		func(s *control.Submission) {
			s.Candidate = "different"
		}, func(s *control.Submission) {
			s.TaskID = "different"
		},
		func(s *control.Submission) {
			s.TaskSnapshot = "different"
		}, func(s *control.Submission) {
			s.EvaluationPlan = "different"
		},
		func(s *control.Submission) {
			s.HostBinding.Version = "different"
		}, func(s *control.Submission) {
			s.Kind = eval.ExecutionEvaluation
		},
	}
	for _, change := range variants {
		changed := recoverySubmission()
		change(&changed)
		_, err = e.Accept(t.Context(), changed)
		assertControlCode(t, err, control.RequestConflict)
	}
	scoped := recoverySubmission()
	scoped.Scope = "other"
	other, err := e.Accept(t.Context(), scoped)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, other.ID)
	_, err = e.Status(t.Context(), "other", first.ID)
	assertControlCode(t, err, control.NotFound)
	_, err = e.Cancel(t.Context(), "other", first.ID)
	assertControlCode(t, err, control.NotFound)
	require.NoError(t, e.Close())
	reopened := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		return finishedRecoveryResult(), nil
	})
	same, err = reopened.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	assert.Equal(t, first, same)
	changed := recoverySubmission()
	changed.Candidate = "changed"
	_, err = reopened.Accept(t.Context(), changed)
	assertControlCode(t, err, control.RequestConflict)
}

func TestControlRecoveryExclusiveOwnerAndCorruption(t *testing.T) {
	dir := t.TempDir()
	run := func(context.Context, control.Submission) (testfixture.Result, error) {
		return finishedRecoveryResult(), nil
	}
	e := openRecoveryExecutor(t, dir, run)
	_, err := testfixture.OpenExecutor(testfixture.ExecutorConfig{Dir: dir, Run: run})
	require.Error(t, err)
	job, err := e.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	terminal, err := e.RunQueued(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Succeeded, terminal.State)
	require.NoError(t, e.Close())
	records, err := filepath.Glob(filepath.Join(dir, "*.json"))
	require.NoError(t, err)
	require.Len(t, records, 1)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, root.Close()) })
	name := filepath.Base(records[0])
	data, err := root.ReadFile(name)
	require.NoError(t, err)
	altered := strings.Replace(string(data), `"State":"succeeded"`, `"State":"canceled"`, 1)
	require.NotEqual(t, string(data), altered)
	require.NoError(t, root.WriteFile(name, []byte(altered), 0600))
	_, err = testfixture.OpenExecutor(testfixture.ExecutorConfig{Dir: dir, Run: run})
	require.Error(t, err)
	require.NoError(t, root.WriteFile(name, []byte(`{"truncated":`), 0600))
	_, err = testfixture.OpenExecutor(testfixture.ExecutorConfig{Dir: dir, Run: run})
	require.Error(t, err)
}

func TestControlRecoveryLostOwner(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	old := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		close(started)
		<-release
		return finishedRecoveryResult(), nil
	})
	job, err := old.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, runErr := old.RunQueued(t.Context(), "scope", job.ID)
		done <- runErr
	}()
	<-started
	require.NoError(t, old.Close())
	replacement := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		t.Fatal("lost claim replay")
		return testfixture.Result{}, nil
	})
	close(release)
	require.Error(t, <-done)
	got, err := replacement.Status(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Interrupted, got.State)
	assert.Empty(t, got.Manifest)
}

func openRecoveryExecutor(t *testing.T, dir string, run testfixture.RunFunc) *testfixture.DurableExecutor {
	t.Helper()
	e, err := testfixture.OpenExecutor(testfixture.ExecutorConfig{Dir: dir, Run: run})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, e.Close())
	})
	return e
}
func recoverySubmission() control.Submission {
	return control.Submission{Scope: "scope", RequestKey: "key", Candidate: strings.Repeat("a", 64), TaskID: "task", TaskSnapshot: "snapshot", Kind: eval.ExecutionProbe, HostBinding: candidate.Binding{ID: "component-host", Version: "v1"}}
}
func finishedRecoveryResult() testfixture.Result {
	return testfixture.Result{Stopped: true, Finalized: true, ExperimentID: "retained-experiment", Manifest: inspect.ManifestRef("retained-manifest")}
}
func appendRecoveryEffect(t *testing.T, dir string) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, root.Close())
	})
	f, err := root.OpenFile("effect.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = fmt.Fprintln(f, "retained external effect")
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())
}
func recoveryEffects(t *testing.T, dir string) int {
	t.Helper()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, root.Close())
	})
	data, err := root.ReadFile("effect.log")
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(data), "retained external effect\n")
}
func assertControlCode(t *testing.T, err error, code control.Code) {
	t.Helper()
	var typed *control.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, code, typed.Code)
}

func TestControlRecoveryConcurrentDispatch(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	e := openRecoveryExecutor(t, t.TempDir(), func(context.Context, control.Submission) (testfixture.Result, error) {
		close(started)
		<-release
		return finishedRecoveryResult(), nil
	})
	job, err := e.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	done := make(chan control.JobRecord, 1)
	go func() {
		result, runErr := e.RunQueued(t.Context(), "scope", job.ID)
		assert.NoError(t, runErr)
		done <- result
	}()
	<-started
	concurrent, err := e.RunQueued(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Running, concurrent.State)
	close(release)
	assert.Equal(t, control.Succeeded, (<-done).State)
}

func TestControlRecoveryUnavailableBinding(t *testing.T) {
	dir := t.TempDir()
	first := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		t.Fatal("accept dispatched execution")
		return testfixture.Result{}, nil
	})
	job, err := first.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	require.NoError(t, first.Close())
	replacement := openRecoveryExecutor(t, dir, func(_ context.Context, submission control.Submission) (testfixture.Result, error) {
		assert.Equal(t, "snapshot", submission.TaskSnapshot)
		// Host registry resolution fails before preparation/effects, never substitutes a new binding.
		return testfixture.Result{Stopped: true, Finalized: true}, errors.New("recorded binding unavailable")
	})
	failed, err := replacement.RunQueued(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Failed, failed.State)
	assert.Zero(t, recoveryEffects(t, dir))
}
