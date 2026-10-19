package agentfeatures_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	controlfixture "github.com/CaliLuke/loom-mcp/v2/eval/control/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type experimentControllerFixture struct {
	executor          *controlfixture.DurableExecutor
	controller        *experimentController
	harness           *agenttest.Harness
	firstDone         chan struct{}
	secondStarted     chan struct{}
	secondDone        chan struct{}
	started           chan struct{}
	allowFinalization chan struct{}
	directories       []string
	candidates        *candidate.Store
	experiments       *eval.Store
	workspace         *inspect.Workspace
}

type experimentController struct {
	firstJob        chan string
	secondJob       chan string
	cancelPolled    chan struct{}
	inspected       []byte
	invalidObserved bool
	comparison      control.Comparison
	step            int
	fixture         *experimentControllerFixture
	draft           control.CandidateInfo
	candidate       string
	baseline        string
	task            string
	cooperativeTask string
	jobID           string
	cancelID        string
}

func TestExperimentControlJourney(t *testing.T) {
	fixture, err := newExperimentControllerFixture(t)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	type completion struct {
		result *agenttest.Result
		err    error
	}
	completed := make(chan completion, 1)
	go func() {
		result, err := fixture.harness.Run(ctx, "experiment.controller", agenttest.User("Discover the baseline, author and repair a helper, run it, inspect its output, then cancel a cooperative trial."))
		completed <- completion{result: result, err: err}
	}()
	firstID := awaitExperimentValue(t, ctx, fixture.controller.firstJob)
	first, err := fixture.executor.RunQueued(ctx, "controller", firstID)
	require.NoError(t, err)
	assert.Equal(t, control.Succeeded, first.State)
	answer, err := fixture.experiments.ReadArtifact(ctx, first.ExperimentID, first.Attempt, "answer.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("Computed: new:LÓOM\n"), answer)
	require.NotEmpty(t, first.Manifest)
	close(fixture.firstDone)
	secondID := awaitExperimentValue(t, ctx, fixture.controller.secondJob)
	secondResult := make(chan control.JobRecord, 1)
	secondError := make(chan error, 1)
	go func() {
		result, err := fixture.executor.RunQueued(ctx, "controller", secondID)
		secondResult <- result
		secondError <- err
	}()
	awaitExperimentSignal(t, ctx, fixture.started)
	close(fixture.secondStarted)
	awaitExperimentSignal(t, ctx, fixture.controller.cancelPolled)
	canceling, err := fixture.executor.Status(ctx, "controller", secondID)
	require.NoError(t, err)
	assert.Equal(t, control.CancelRequested, canceling.State)
	close(fixture.allowFinalization)
	second := awaitExperimentValue(t, ctx, secondResult)
	require.NoError(t, awaitExperimentValue(t, ctx, secondError))
	assert.Equal(t, control.Canceled, second.State)
	close(fixture.secondDone)
	final := awaitExperimentValue(t, ctx, completed)
	require.NoError(t, final.err)
	require.NotNil(t, final.result)
	require.NoError(t, final.result.RunErr)
	require.NoError(t, fixture.harness.Close())
	assert.True(t, fixture.controller.invalidObserved)
	assert.Equal(t, "not_comparable", fixture.controller.comparison.Status)
	assert.Nil(t, fixture.controller.comparison.Baseline.Quality)
	assert.Nil(t, fixture.controller.comparison.Candidate.Quality)
	assert.Equal(t, []byte("Computed: new:LÓOM\n"), fixture.controller.inspected)
	require.NotNil(t, final.result.Output)
	assert.Contains(t, experimentMessageText(final.result.Output.Final), "Computed: new:LÓOM")
	assert.Contains(t, experimentMessageText(final.result.Output.Final), "canceled")
	frozen, err := fixture.candidates.Load(ctx, fixture.controller.candidate)
	require.NoError(t, err)
	submitted, err := frozen.Read("helper.go")
	require.NoError(t, err)
	assert.Equal(t, controllerHelperSource, string(submitted))
	captures, err := fixture.experiments.ReadEvidence(ctx, first.ExperimentID)
	require.NoError(t, err)
	require.Len(t, captures, 1)
	calls := captures[0].Snapshot.Records[0].Runs[0].Calls
	require.Len(t, calls, 4)
	require.NoError(t, agenttest.LastToolResult(tools.Ident("components.run_helper"), "new:LÓOM")(calls[3].Request))
	for _, directory := range fixture.directories {
		_, err := os.Stat(directory)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func awaitExperimentValue[T any](t *testing.T, ctx context.Context, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		var zero T
		return zero
	}
}

func awaitExperimentSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func experimentMessageText(message *model.Message) string {
	if message == nil {
		return ""
	}
	var text string
	for _, part := range message.Parts {
		if value, ok := part.(model.TextPart); ok {
			text += value.Text
		}
	}
	return text
}
