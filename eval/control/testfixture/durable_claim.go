package testfixture

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/eval/control"
)

// claim serializes dispatch and persists ownership before any execution callback.
func (e *DurableExecutor) claim(ctx context.Context, scope, id string) (diskRecord, context.Context, context.CancelFunc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, err := e.lookup(ctx, scope, id)
	if err != nil {
		return diskRecord{}, nil, nil, err
	}
	if record.Job.State != control.Queued {
		return record, nil, nil, nil
	}
	token, err := randomToken()
	if err != nil {
		return diskRecord{}, nil, nil, err
	}
	record.Claim = token
	record.Job.State = control.Running
	if err = e.persist(record); err != nil {
		return diskRecord{}, nil, nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	e.active[id] = cancel
	return record, runCtx, cancel, nil
}

// complete commits results only for the still-owned claim, after cancellation arbitration.
func (e *DurableExecutor) complete(scope, id, claim string, result Result, runErr error) (control.JobRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, err := e.lookup(context.Background(), scope, id)
	if err != nil {
		return control.JobRecord{}, err
	}
	if current.Claim != claim || (current.Job.State != control.Running && current.Job.State != control.CancelRequested) {
		return control.JobRecord{}, errors.New("execution claim lost")
	}
	current.Job.State = terminalState(current.Job.State, result, runErr)
	if current.Job.State == control.Failed || current.Job.State == control.Interrupted {
		current.Job.Failure = control.NewError(control.OperationFailed, "", "", "")
	}
	current.Job.ExperimentID = result.ExperimentID
	current.Job.Attempt = result.Attempt
	current.Job.Manifest = result.Manifest
	current.Claim = ""
	if err = e.persist(current); err != nil {
		return control.JobRecord{}, err
	}
	delete(e.active, id)
	return cloneJob(current.Job), nil
}
