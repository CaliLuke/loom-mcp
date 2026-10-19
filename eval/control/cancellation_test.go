package control_test

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/control/testfixture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlCancellationQueued(t *testing.T) {
	dir := t.TempDir()
	e := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		t.Fatal("canceled queued job dispatched")
		return testfixture.Result{}, nil
	})
	job, err := e.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	canceled, err := e.Cancel(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Canceled, canceled.State)
	again, err := e.RunQueued(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, canceled, again)
	require.NoError(t, e.Close())
	reopened := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		t.Fatal("canceled replay")
		return testfixture.Result{}, nil
	})
	again, err = reopened.Cancel(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, canceled, again)
}

func TestControlCancellationRunningArbitration(t *testing.T) {
	cases := []struct {
		name   string
		result testfixture.Result
		runErr error
		want   control.JobState
	}{
		{"finalized", testfixture.Result{Stopped: true, Finalized: true, Canceled: true}, nil, control.Canceled},
		{"cancel_first_success_result", testfixture.Result{Stopped: true, Finalized: true}, nil, control.Canceled},
		{"cleanup_failure", testfixture.Result{Stopped: true, Canceled: true}, errors.New("release failed"), control.Failed},
		{"unfinalized", testfixture.Result{Stopped: true}, nil, control.Failed},
		{"unknown_stoppedness", testfixture.Result{Finalized: true, Canceled: true}, nil, control.Interrupted},
		{"unknown_with_failure", testfixture.Result{Finalized: true}, errors.New("stop failed"), control.Interrupted},
		{"cancellation_error_alone", testfixture.Result{Stopped: true, Finalized: true}, context.Canceled, control.Failed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			signaled := make(chan struct{})
			release := make(chan struct{})
			dir := t.TempDir()
			e := openRecoveryExecutor(t, dir, func(ctx context.Context, _ control.Submission) (testfixture.Result, error) {
				close(started)
				<-ctx.Done()
				close(signaled)
				<-release
				return tc.result, tc.runErr
			})
			job, err := e.Accept(t.Context(), recoverySubmission())
			require.NoError(t, err)
			done := make(chan control.JobRecord, 1)
			go func() {
				got, runErr := e.RunQueued(t.Context(), "scope", job.ID)
				assert.NoError(t, runErr)
				done <- got
			}()
			<-started
			requested, err := e.Cancel(t.Context(), "scope", job.ID)
			require.NoError(t, err)
			assert.Equal(t, control.CancelRequested, requested.State)
			<-signaled
			pending, err := e.Status(t.Context(), "scope", job.ID)
			require.NoError(t, err)
			assert.Equal(t, control.CancelRequested, pending.State)
			close(release)
			terminal := <-done
			assert.Equal(t, tc.want, terminal.State)
			again, err := e.Cancel(t.Context(), "scope", job.ID)
			require.NoError(t, err)
			assert.Equal(t, terminal, again)
			require.NoError(t, e.Close())
			reopened := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
				t.Fatal("terminal cancellation outcome replayed")
				return testfixture.Result{}, nil
			})
			persisted, err := reopened.Cancel(t.Context(), "scope", job.ID)
			require.NoError(t, err)
			assert.Equal(t, terminal, persisted)
		})
	}
}

func TestControlCancellationRaceSuccessFirst(t *testing.T) {
	e := openRecoveryExecutor(t, t.TempDir(), func(context.Context, control.Submission) (testfixture.Result, error) {
		return finishedRecoveryResult(), nil
	})
	job, err := e.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	success, err := e.RunQueued(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Succeeded, success.State)
	canceled, err := e.Cancel(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, success, canceled)
}

func TestControlCancellationRequestContextAlone(t *testing.T) {
	e := openRecoveryExecutor(t, t.TempDir(), func(context.Context, control.Submission) (testfixture.Result, error) {
		return finishedRecoveryResult(), nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	job, err := e.Accept(ctx, recoverySubmission())
	require.NoError(t, err)
	cancel()
	got, err := e.Status(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Queued, got.State)
}

func TestControlCancellationRestartPending(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	old := openRecoveryExecutor(t, dir, func(ctx context.Context, _ control.Submission) (testfixture.Result, error) {
		close(started)
		<-ctx.Done()
		<-release
		return testfixture.Result{Stopped: true, Finalized: true, Canceled: true}, nil
	})
	job, err := old.Accept(t.Context(), recoverySubmission())
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, runErr := old.RunQueued(t.Context(), "scope", job.ID)
		done <- runErr
	}()
	<-started
	requested, err := old.Cancel(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.CancelRequested, requested.State)
	require.NoError(t, old.Close())
	replacement := openRecoveryExecutor(t, dir, func(context.Context, control.Submission) (testfixture.Result, error) {
		t.Fatal("pending cancellation replayed")
		return testfixture.Result{}, nil
	})
	interrupted, err := replacement.Status(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, control.Interrupted, interrupted.State)
	close(release)
	require.Error(t, <-done)
	again, err := replacement.Cancel(t.Context(), "scope", job.ID)
	require.NoError(t, err)
	assert.Equal(t, interrupted, again)
}
