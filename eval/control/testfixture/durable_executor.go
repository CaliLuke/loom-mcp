// Package testfixture provides an explicitly driven durable host executor for control tests.
package testfixture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

// CrashPoint names a durable boundary for deterministic process crash injection.
type CrashPoint string

// Result describes established execution and finalization facts, not cancellation intent.
type Result struct {
	Stopped      bool
	Finalized    bool
	Canceled     bool
	ExperimentID string
	Attempt      eval.AttemptKey
	Manifest     inspect.ManifestRef
}

// RunFunc executes a host-resolved immutable submission and finalizes its evidence.
// The host must reject unavailable recorded task/binding versions before effects.
type RunFunc func(context.Context, control.Submission) (Result, error)

// ExecutorConfig binds one exclusively owned disk directory and a host runner.
type ExecutorConfig struct {
	Dir        string
	Run        RunFunc
	Checkpoint func(CrashPoint) error
}

// DurableExecutor retains scoped request identity and job state across host replacement.
// Only RunQueued dispatches work; opening or accepting never starts a worker.
type DurableExecutor struct {
	mu      sync.Mutex
	cfg     ExecutorConfig
	lock    *os.File
	records map[string]diskRecord
	active  map[string]context.CancelFunc
	closed  bool
	fault   error
}

type diskRecord struct {
	Version   int
	Key       string
	Identity  string
	Integrity string
	Claim     string
	Job       control.JobRecord
}

const (
	// BeforeAcceptance precedes any durable request binding.
	BeforeAcceptance CrashPoint = "before_acceptance"
	// AfterAcceptance follows durable request binding, before acknowledgement.
	AfterAcceptance CrashPoint = "after_acceptance"
	// AfterClaim follows a durable claim and precedes entry to the runner.
	AfterClaim CrashPoint = "after_claim"
	// AfterEffect is called by the host runner after its real external effect.
	AfterEffect CrashPoint = "after_effect"
	// BeforeCompletion precedes terminal commit and permits cancellation arbitration.
	BeforeCompletion CrashPoint = "before_completion"
	// AfterCompletion follows durable terminal commit, before returning to the host.
	AfterCompletion CrashPoint = "after_completion"
)

// OpenExecutor acquires exclusive host ownership and interrupts lost execution claims.
func OpenExecutor(cfg ExecutorConfig) (_ *DurableExecutor, err error) {
	if cfg.Dir == "" || cfg.Run == nil {
		return nil, control.NewError(control.ExecutorUnavailable, "", "", "")
	}
	if err = os.MkdirAll(cfg.Dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(cfg.Dir, "owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, lock.Close())
		}
	}()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("acquire executor ownership: %w", err)
	}
	e := &DurableExecutor{cfg: cfg, lock: lock, records: make(map[string]diskRecord), active: make(map[string]context.CancelFunc)}
	if err = e.loadRecords(); err != nil {
		return nil, err
	}
	if err = e.interruptLostClaims(); err != nil {
		return nil, err
	}
	return e, nil
}

// Accept atomically binds a scoped request key before acknowledging a job.
func (e *DurableExecutor) Accept(ctx context.Context, submission control.Submission) (control.JobRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.available(ctx); err != nil {
		return control.JobRecord{}, err
	}
	if submission.Scope == "" || submission.RequestKey == "" {
		return control.JobRecord{}, control.NewError(control.InvalidInput, "", "", "request_key")
	}
	if submission.Kind == "" {
		submission.Kind = eval.ExecutionProbe
	}
	key, identity, err := submissionIdentity(submission)
	if err != nil {
		return control.JobRecord{}, err
	}
	for _, record := range e.records {
		if record.Key == key {
			if record.Identity != identity {
				return control.JobRecord{}, control.NewError(control.RequestConflict, "", "", "request_key")
			}
			return cloneJob(record.Job), nil
		}
	}
	if err = e.checkpoint(BeforeAcceptance); err != nil {
		return control.JobRecord{}, err
	}
	id, err := randomToken()
	if err != nil {
		return control.JobRecord{}, err
	}
	record := diskRecord{Version: 1, Key: key, Identity: identity, Job: control.JobRecord{ID: id, Submission: submission, State: control.Queued}}
	if err = e.persist(record); err != nil {
		return control.JobRecord{}, err
	}
	if err = e.checkpoint(AfterAcceptance); err != nil {
		return control.JobRecord{}, err
	}
	return cloneJob(record.Job), nil
}

// Status returns the persisted job only within its accepted scope.
func (e *DurableExecutor) Status(ctx context.Context, scope, id string) (control.JobRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, err := e.lookup(ctx, scope, id)
	if err != nil {
		return control.JobRecord{}, err
	}
	return cloneJob(record.Job), nil
}

// Cancel persists cancellation intent before signaling owned execution.
func (e *DurableExecutor) Cancel(ctx context.Context, scope, id string) (control.JobRecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	record, err := e.lookup(ctx, scope, id)
	if err != nil {
		return control.JobRecord{}, err
	}
	switch record.Job.State {
	case control.Queued:
		record.Job.State = control.Canceled
	case control.Running:
		record.Job.State = control.CancelRequested
	case control.CancelRequested, control.Succeeded, control.Failed, control.Canceled, control.Interrupted:
		return cloneJob(record.Job), nil
	default:
		return control.JobRecord{}, errors.New("invalid durable job state")
	}
	if err = e.persist(record); err != nil {
		return control.JobRecord{}, err
	}
	if cancel := e.active[id]; cancel != nil {
		cancel()
	}
	return cloneJob(record.Job), nil
}

// RunQueued explicitly claims and executes one queued job; no worker starts implicitly.
func (e *DurableExecutor) RunQueued(ctx context.Context, scope, id string) (control.JobRecord, error) {
	record, runCtx, cancel, err := e.claim(ctx, scope, id)
	if err != nil {
		return control.JobRecord{}, err
	}
	if cancel == nil {
		return cloneJob(record.Job), nil
	}
	defer cancel()
	if err = e.checkpoint(AfterClaim); err != nil {
		return control.JobRecord{}, err
	}
	result, runErr := e.cfg.Run(runCtx, record.Job.Submission)
	if err = e.checkpoint(BeforeCompletion); err != nil {
		return control.JobRecord{}, err
	}
	job, err := e.complete(scope, id, record.Claim, result, runErr)
	if err != nil {
		return control.JobRecord{}, err
	}
	if err = e.checkpoint(AfterCompletion); err != nil {
		return control.JobRecord{}, err
	}
	return job, nil
}

// Close fences this owner and releases exclusive directory ownership.
// In-flight runners may return later, but can no longer publish their results.
func (e *DurableExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	for _, cancel := range e.active {
		cancel()
	}
	return e.lock.Close()
}

func terminalState(state control.JobState, result Result, runErr error) control.JobState {
	switch {
	case !result.Stopped:
		return control.Interrupted
	case runErr != nil || !result.Finalized:
		return control.Failed
	case state == control.CancelRequested || result.Canceled:
		return control.Canceled
	default:
		return control.Succeeded
	}
}
