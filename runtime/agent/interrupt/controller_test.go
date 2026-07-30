package interrupt

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControllerRoutesSignalsToMatchingReceivers(t *testing.T) {
	pause := &receiverStub[*api.PauseRequest]{value: &api.PauseRequest{}, asyncOK: true}
	resume := &receiverStub[*api.ResumeRequest]{value: &api.ResumeRequest{}}
	clarify := &receiverStub[*api.ClarificationAnswer]{value: &api.ClarificationAnswer{}}
	results := &receiverStub[*api.ToolResultsSet]{value: &api.ToolResultsSet{}}
	confirm := &receiverStub[*api.ConfirmationDecision]{value: &api.ConfirmationDecision{}}
	typed := &receiverStub[*api.TypedInputAnswer]{value: &api.TypedInputAnswer{}}
	controller := &Controller{
		pauseCh:      pause,
		resumeCh:     resume,
		clarifyCh:    clarify,
		resultsCh:    results,
		confirmCh:    confirm,
		typedInputCh: typed,
	}
	ctx := context.Background()
	timeout := 2 * time.Second

	gotPause, ok := controller.PollPause()
	assert.True(t, ok)
	assert.Same(t, pause.value, gotPause)

	gotResume, err := controller.WaitResume(ctx, timeout)
	require.NoError(t, err)
	assert.Same(t, resume.value, gotResume)

	gotClarification, err := controller.WaitProvideClarification(ctx, timeout)
	require.NoError(t, err)
	assert.Same(t, clarify.value, gotClarification)

	gotResults, err := controller.WaitProvideToolResults(ctx, timeout)
	require.NoError(t, err)
	assert.Same(t, results.value, gotResults)

	gotConfirmation, err := controller.WaitProvideConfirmation(ctx, timeout)
	require.NoError(t, err)
	assert.Same(t, confirm.value, gotConfirmation)

	gotTypedInput, err := controller.WaitProvideTypedInput(ctx, timeout)
	require.NoError(t, err)
	assert.Same(t, typed.value, gotTypedInput)

	assert.Equal(t, 1, pause.asyncCalls)
	assertTimeoutCall(t, resume, timeout)
	assertTimeoutCall(t, clarify, timeout)
	assertTimeoutCall(t, results, timeout)
	assertTimeoutCall(t, confirm, timeout)
	assertTimeoutCall(t, typed, timeout)
}

func TestControllerUsesBlockingReceiveWithoutTimeout(t *testing.T) {
	resume := &receiverStub[*api.ResumeRequest]{value: &api.ResumeRequest{}}
	clarify := &receiverStub[*api.ClarificationAnswer]{value: &api.ClarificationAnswer{}}
	results := &receiverStub[*api.ToolResultsSet]{value: &api.ToolResultsSet{}}
	confirm := &receiverStub[*api.ConfirmationDecision]{value: &api.ConfirmationDecision{}}
	typed := &receiverStub[*api.TypedInputAnswer]{value: &api.TypedInputAnswer{}}
	controller := &Controller{
		resumeCh:     resume,
		clarifyCh:    clarify,
		resultsCh:    results,
		confirmCh:    confirm,
		typedInputCh: typed,
	}

	got, err := controller.WaitResume(context.Background(), 0)
	require.NoError(t, err)
	assert.Same(t, resume.value, got)

	_, err = controller.WaitProvideClarification(context.Background(), -time.Second)
	require.NoError(t, err)
	_, err = controller.WaitProvideToolResults(context.Background(), 0)
	require.NoError(t, err)
	_, err = controller.WaitProvideConfirmation(context.Background(), 0)
	require.NoError(t, err)
	_, err = controller.WaitProvideTypedInput(context.Background(), 0)
	require.NoError(t, err)

	assertBlockingCall(t, resume)
	assertBlockingCall(t, clarify)
	assertBlockingCall(t, results)
	assertBlockingCall(t, confirm)
	assertBlockingCall(t, typed)
}

func TestControllerPropagatesCancellationAndTimeoutErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resume := &receiverStub[*api.ResumeRequest]{receiveErr: context.Canceled}
	controller := &Controller{resumeCh: resume}

	_, err := controller.WaitResume(ctx, 0)
	require.ErrorIs(t, err, context.Canceled)

	clarify := &receiverStub[*api.ClarificationAnswer]{timeoutErr: context.DeadlineExceeded}
	controller.clarifyCh = clarify
	_, err = controller.WaitProvideClarification(context.Background(), time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

type receiverStub[T any] struct {
	value        T
	receiveErr   error
	timeoutErr   error
	asyncOK      bool
	receiveCalls int
	timeoutCalls int
	asyncCalls   int
	lastTimeout  time.Duration
}

func (r *receiverStub[T]) Receive(context.Context) (T, error) {
	r.receiveCalls++
	return r.value, r.receiveErr
}

func (r *receiverStub[T]) ReceiveWithTimeout(_ context.Context, timeout time.Duration) (T, error) {
	r.timeoutCalls++
	r.lastTimeout = timeout
	return r.value, r.timeoutErr
}

func (r *receiverStub[T]) ReceiveAsync() (T, bool) {
	r.asyncCalls++
	return r.value, r.asyncOK
}

func assertTimeoutCall[T any](t *testing.T, receiver *receiverStub[T], timeout time.Duration) {
	t.Helper()
	assert.Equal(t, 1, receiver.timeoutCalls)
	assert.Zero(t, receiver.receiveCalls)
	assert.Equal(t, timeout, receiver.lastTimeout)
}

func assertBlockingCall[T any](t *testing.T, receiver *receiverStub[T]) {
	t.Helper()
	assert.Equal(t, 1, receiver.receiveCalls)
	assert.Zero(t, receiver.timeoutCalls)
}
