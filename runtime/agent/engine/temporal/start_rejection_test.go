package temporal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
)

func TestStartWorkflowClassifiesLocalPreflightRejections(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)
	_, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{
		ID: "missing-workflow", Workflow: "missing.workflow",
	})
	require.ErrorIs(t, err, engine.ErrWorkflowStartRejected)
	require.ErrorContains(t, err, `workflow "missing.workflow" is not registered`)

	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "registered.workflow",
		Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
			return &api.RunOutput{}, nil
		},
	}))
	_, err = eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{
		ID: "unsupported-attribute", Workflow: "registered.workflow",
		SearchAttributes: map[string]any{"Unsupported": []int{1}},
	})
	require.ErrorIs(t, err, engine.ErrWorkflowStartRejected)
	assert.ErrorContains(t, err, `search attribute "Unsupported" has unsupported type []int`)
}

func TestStartWorkflowClassifiesTemporalServerRejections(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		server error
		reject bool
		wantAs any
	}{
		{name: "invalid argument", server: &serviceerror.InvalidArgument{Message: "invalid request"}, wantAs: new(*serviceerror.InvalidArgument)},
		{name: "namespace missing", server: &serviceerror.NamespaceNotFound{Namespace: "missing", Message: "namespace not found"}, wantAs: new(*serviceerror.NamespaceNotFound)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &testWorkflowService{startErr: tt.server}
			eng, err := NewWorker(Options{
				Client:        newWorkflowServiceClient(t, service),
				WorkerOptions: WorkerOptions{TaskQueue: "default.queue"},
			})
			require.NoError(t, err)
			require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
				Name: "registered.workflow",
				Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
					return &api.RunOutput{}, nil
				},
			}))

			_, err = eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{
				ID: "run", Workflow: "registered.workflow",
			})
			require.ErrorIs(t, err, engine.ErrWorkflowStartRejected)
			assert.ErrorAs(t, err, tt.wantAs)
		})
	}
}

func TestClassifyTemporalStartErrorOnlyMarksDefiniteRejections(t *testing.T) {
	t.Parallel()

	invalidArgument := &serviceerror.InvalidArgument{Message: "invalid request"}
	namespaceNotFound := &serviceerror.NamespaceNotFound{Namespace: "missing", Message: "namespace not found"}
	alreadyStarted := serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "request", "run")
	for _, tt := range []struct {
		name   string
		err    error
		reject bool
		wantAs any
		wantIs error
	}{
		{name: "invalid argument", err: invalidArgument, reject: true, wantAs: new(*serviceerror.InvalidArgument)},
		{name: "namespace missing", err: namespaceNotFound, reject: true, wantAs: new(*serviceerror.NamespaceNotFound)},
		{name: "already started", err: alreadyStarted, wantAs: new(*serviceerror.WorkflowExecutionAlreadyStarted)},
		{name: "unavailable", err: &serviceerror.Unavailable{Message: "unavailable"}, wantAs: new(*serviceerror.Unavailable)},
		{name: "joined mixed outcome", err: errors.Join(invalidArgument, &serviceerror.Unavailable{Message: "unavailable"})},
		{name: "wrapped rejection remains conservative", err: fmt.Errorf("wrapped: %w", invalidArgument)},
		{name: "internal", err: &serviceerror.Internal{Message: "internal"}, wantAs: new(*serviceerror.Internal)},
		{name: "canceled", err: context.Canceled, wantIs: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, wantIs: context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTemporalStartError(tt.err)
			if tt.wantIs != nil {
				require.ErrorIs(t, got, tt.wantIs)
			}
			if tt.reject {
				require.ErrorIs(t, got, engine.ErrWorkflowStartRejected)
				assert.ErrorAs(t, got, tt.wantAs)
			} else {
				assert.NotErrorIs(t, got, engine.ErrWorkflowStartRejected)
				if tt.wantAs != nil {
					assert.ErrorAs(t, got, tt.wantAs)
				}
			}
		})
	}
}
