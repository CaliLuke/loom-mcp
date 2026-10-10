package temporal

import (
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/log"
	temporalsdk "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// ReplayWorkflowHistory replays a recorded history against a snapshot of all
// registered workflows, using the worker's handler wrapping and data converter.
// Register all agents first; no server, running worker, or activities are needed.
// Load history JSON with client.HistoryFromJSON. The logger may be nil.
//
// When using an external Client, supply Options.ClientOptions with that client's
// DataConverter; otherwise replay fails. Protocol mismatches retain their original
// message even when the SDK reports a subsequent command/history mismatch.
func (e *Engine) ReplayWorkflowHistory(logger log.Logger, history *historypb.History) error {
	if e.replayDataConverter == nil {
		return fmt.Errorf("temporal engine: replay requires ClientOptions with the worker client's DataConverter")
	}
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		DataConverter:               e.replayDataConverter,
		Interceptors:                e.workerOpts.Interceptors,
		DisableRegistrationAliasing: e.workerOpts.DisableRegistrationAliasing,
	})
	if err != nil {
		return err
	}
	var protocolErr error
	e.mu.Lock()
	for _, def := range e.workflows {
		wrapped := e.wrapWorkflowHandler(def)
		replayer.RegisterWorkflowWithOptions(func(ctx workflow.Context, input *api.RunInput) (*api.RunOutput, error) {
			out, handlerErr := wrapped(ctx, input)
			var appErr *temporalsdk.ApplicationError
			if errors.As(handlerErr, &appErr) && appErr.Type() == workflowProtocolMismatchFailureType {
				protocolErr = handlerErr
			}
			return out, handlerErr
		}, workflow.RegisterOptions{Name: def.Name})
	}
	e.mu.Unlock()
	err = replayer.ReplayWorkflowHistory(logger, history)
	if protocolErr != nil {
		return protocolErr
	}
	return err
}

// wrapWorkflowHandler keeps live execution and replay on the same context and
// failure-conversion path, including non-retryable protocol rejection.
func (e *Engine) wrapWorkflowHandler(def engine.WorkflowDefinition) func(workflow.Context, *api.RunInput) (*api.RunOutput, error) {
	return func(tctx workflow.Context, input *api.RunInput) (*api.RunOutput, error) {
		wfCtx := newTemporalWorkflowContext(e, tctx)
		defer e.releaseWorkflowContext(wfCtx.runID, wfCtx)
		out, err := def.Handler(wfCtx, input)
		classified := classifyTemporalCancellation(err)
		if errors.Is(classified, api.ErrWorkflowProtocolMismatch) {
			return out, temporalsdk.NewNonRetryableApplicationError(err.Error(), workflowProtocolMismatchFailureType, nil)
		}
		return out, classified
	}
}
