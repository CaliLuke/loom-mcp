package enginetest

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/require"
)

func testGenericActivityRegistration(t *testing.T, f Fixture) error {
	var nilHandler func(context.Context, metadataInput) (metadataOutput, error)
	for _, handler := range []any{nil, nilHandler, 42, func(metadataInput) error {
		return nil
	}, func(context.Context, metadataInput) string {
		return "invalid"
	}} {
		require.Error(t, f.Engine.RegisterActivity(f.ctx, engine.ActivityDefinition{Name: activityName, Handler: handler}))
	}
	activity := engine.Activity[metadataInput, metadataOutput]{Name: activityName}
	handler := func(context.Context, metadataInput) (metadataOutput, error) {
		return metadataOutput{Size: 42}, nil
	}
	require.Error(t, (engine.Activity[metadataInput, metadataOutput]{}).Register(f.ctx, f.Engine, activityOptions(f), handler))
	require.NoError(t, activity.Register(f.ctx, f.Engine, activityOptions(f), handler))
	require.Error(t, activity.Register(f.ctx, f.Engine, activityOptions(f), handler))
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		out, err := activity.Execute(w.Context(), w, metadataInput{Key: "upload"}, engine.ActivityOptions{})
		if err != nil {
			return nil, err
		}
		return success(), checkEqual(metadataOutput{Size: 42}, out)
	})
	return waitSuccess(f.ctx, start(t, f, f.ctx))
}

func testGenericActivityNilResult(t *testing.T, f Fixture) error {
	activity := engine.Activity[metadataInput, any]{Name: activityName}
	require.NoError(t, activity.Register(f.ctx, f.Engine, activityOptions(f), func(context.Context, metadataInput) (any, error) {
		return nil, nil
	}))
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		out, err := activity.Execute(w.Context(), w, metadataInput{Key: "missing"}, engine.ActivityOptions{})
		if err != nil {
			return nil, err
		}
		if err := checkEqual(nil, out); err != nil {
			return nil, err
		}
		future, err := activity.ExecuteAsync(w.Context(), w, metadataInput{Key: "missing"}, engine.ActivityOptions{})
		if err != nil {
			return nil, err
		}
		out, err = future.Get(w.Context())
		if err != nil {
			return nil, err
		}
		return success(), checkEqual(nil, out)
	})
	return waitSuccess(f.ctx, start(t, f, f.ctx))
}
