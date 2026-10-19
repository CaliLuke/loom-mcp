package eval

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"slices"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

// runPreparedAttempt owns the acquired lease until every finalization phase ends.
func runPreparedAttempt(ctx context.Context, cfg Config, job runnerJob, kind ExecutionKind) *AttemptRecord {
	a := job.attempt
	r := &AttemptRecord{AttemptKey: a.key, Index: job.index, ExecutionKind: kind}
	if kind == ExecutionProbe {
		r.Probe = initialProbeStatus()
	}
	if ctx.Err() != nil {
		r.NotRun = StopCanceled
		return r
	}
	r.Started = time.Now()
	r.Outcome = &Outcome{Scenario: a.key.Scenario, Item: a.key.Item}
	defer func() {
		r.Duration = time.Since(r.Started)
	}()
	lease, dir := acquirePreparedLease(ctx, cfg, a, r)
	if lease == nil {
		markProbeExecution(r, ctx)
		return r
	}
	executePreparedLease(ctx, cfg, a, r, lease, dir, kind)
	return r
}

// executePreparedLease installs, executes and finalizes one acquired lease.
func executePreparedLease(ctx context.Context, cfg Config, a plannedAttempt, r *AttemptRecord, lease *PreparedAttempt, dir string, kind ExecutionKind) {
	var h *agenttest.Harness
	defer func() {
		markProbeExecution(r, ctx)
		finalizePrepared(ctx, cfg.CleanupTimeout, lease, r, cfg.PreparedWriter, dir, h)
	}()
	if r.HarnessErr != nil {
		return
	}
	if err := installPreparedLease(ctx, cfg.PreparedWriter, a, lease, r); err != nil {
		r.HarnessErr = err
		return
	}
	h, err := constructPreparedHarness(ctx, a, lease, dir)
	r.HarnessErr = phaseError("construct", err)
	if h == nil {
		return
	}
	s := a.scenario
	if lease.Drive != nil {
		s.Drive = lease.Drive
	} else {
		s.BuildInputs = func(context.Context, Item) ([]agenttest.Input, error) {
			return lease.Inputs, nil
		}
	}
	out, executeErr := executeResolved(ctx, h, s, resolvedItem(s, a.item), kind == ExecutionProbe)
	if out != nil {
		r.Outcome = out
	}
	r.Outcome.Err = errors.Join(r.Outcome.Err, executeErr)
	r.countUsage()
}

// installPreparedLease preserves valid installation evidence even when inputs fail.
func installPreparedLease(ctx context.Context, w *Writer, a plannedAttempt, lease *PreparedAttempt, r *AttemptRecord) error {
	leaseErr := validatePreparedLease(lease, a.scenario.Agent)
	if leaseErr == nil {
		leaseErr = phaseError("inputs", ownPreparedInputs(lease))
	}
	if validatePreparedReceipt(lease.Receipt) != nil {
		return leaseErr
	}
	if err := w.writePreparedReceipt(a.key, lease.Receipt); err != nil {
		return errors.Join(leaseErr, phaseError("receipt", err))
	}
	r.Prepared = clonePreparedReceipt(&lease.Receipt)
	return errors.Join(leaseErr, context.Cause(ctx))
}

// acquirePreparedLease reserves durable identity before acquiring host state.
func acquirePreparedLease(ctx context.Context, cfg Config, a plannedAttempt, r *AttemptRecord) (*PreparedAttempt, string) {
	dir, err := cfg.PreparedWriter.reservePrepared(a.key)
	if err != nil {
		r.HarnessErr = err
		return nil, ""
	}
	prepareCtx, cancel := context.WithTimeout(ctx, cfg.PrepareTimeout)
	lease, prepareErr := callPreparation(prepareCtx, cfg.PrepareAttempt, preparedInfo(a))
	r.HarnessErr = phaseError("prepare", errors.Join(prepareErr, context.Cause(prepareCtx)))
	cancel()
	if lease == nil {
		if r.HarnessErr == nil {
			r.HarnessErr = errors.New("eval: prepare returned a nil lease")
		}
		return nil, dir
	}
	owned := *lease
	owned.Deliverables = slices.Clone(lease.Deliverables)
	owned.Receipt = *clonePreparedReceipt(&lease.Receipt)
	if r.HarnessErr != nil && validatePreparedReceipt(owned.Receipt) == nil {
		if err := cfg.PreparedWriter.writePreparedReceipt(a.key, owned.Receipt); err != nil {
			r.HarnessErr = errors.Join(r.HarnessErr, phaseError("receipt", err))
		} else {
			r.Prepared = clonePreparedReceipt(&owned.Receipt)
		}
	}
	return &owned, dir
}

func constructPreparedHarness(ctx context.Context, a plannedAttempt, lease *PreparedAttempt, dir string) (*agenttest.Harness, error) {
	factory := NewAttemptHarnessFunc(func(context.Context, AttemptInfo) (AttemptHarnessConfig, error) {
		return lease.Harness, nil
	}, lease.BaseOptions...)
	opts := append(slices.Clone(a.variant.Options), agenttest.WithDefaultRunOptions(variantRunOptions(a.variant)...), agenttest.WithEvidenceDir(filepath.Join(dir, "capture")))
	return callFactory(ctx, factory, attemptInfo(a), opts)
}

// finalizePrepared retains capture and outputs before releasing host-owned state.
func finalizePrepared(ctx context.Context, timeout time.Duration, lease *PreparedAttempt, r *AttemptRecord, w *Writer, dir string, h *agenttest.Harness) {
	if h != nil {
		r.HarnessErr = errors.Join(r.HarnessErr, phaseError("close", h.Close()))
	}
	quiesceErr := cleanupCallback(ctx, timeout, "quiesce", lease.Quiesce)
	r.HarnessErr = errors.Join(r.HarnessErr, quiesceErr)
	collectPrepared(ctx, timeout, lease, r, w, dir, quiesceErr != nil)
	r.HarnessErr = errors.Join(r.HarnessErr, cleanupCallback(ctx, timeout, "release", lease.Release))
}

// collectPrepared owns a fresh capture and retains outputs before environment release.
func collectPrepared(ctx context.Context, timeout time.Duration, lease *PreparedAttempt, r *AttemptRecord, w *Writer, dir string, provisional bool) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	snapshot, captureErr := agenttest.ReadEvidence(readCtx, filepath.Join(dir, "capture"))
	cancel()
	r.HarnessErr = errors.Join(r.HarnessErr, phaseError("capture", captureErr))
	input := CollectionInput{Key: r.AttemptKey, Receipt: *clonePreparedReceipt(&lease.Receipt), Evidence: snapshot, EvidenceError: captureErr, Provisional: provisional}
	var artifacts []CollectedArtifact
	collectErr := cleanupCallback(ctx, timeout, "collect", func(collectCtx context.Context) error {
		if lease.Collect == nil {
			return errors.New("collection callback is required")
		}
		var err error
		artifacts, err = lease.Collect(collectCtx, input)
		return err
	})
	// Validate and retain the readable output prefix even if the collector failed.
	artifacts, deliveryErr := collectedDeliverables(lease.Deliverables, artifacts)
	var retained []StoredArtifact
	var retainErr error
	if r.Prepared != nil {
		retained, retainErr = w.retainPreparedArtifacts(r.AttemptKey, artifacts, input.Provisional)
	} else if len(artifacts) > 0 {
		retainErr = errors.New("cannot retain artifacts without a durable prepared receipt")
	}
	r.Artifacts = retained
	r.HarnessErr = errors.Join(r.HarnessErr, collectErr, phaseError("collection", deliveryErr), phaseError("retain", retainErr))
	markProbeEvidence(r.Probe, len(lease.Deliverables), len(retained), provisional, errors.Join(captureErr, collectErr, deliveryErr, retainErr), errors.Join(deliveryErr, retainErr))
}

func markProbeEvidence(probe *ProbeStatus, declared, retained int, provisional bool, evidenceErr, deliveryErr error) {
	if probe == nil {
		return
	}
	if declared > 0 {
		probe.Delivery = probeStatusComplete
		if deliveryErr != nil || retained != declared {
			probe.Delivery = probeStatusIncomplete
		}
	}
	probe.Evidence = probeStatusComplete
	if evidenceErr != nil {
		probe.Evidence = probeStatusIncomplete
	}
	if provisional {
		probe.Evidence = probeEvidenceProvisional
	}
}

// cleanupCallback gives each cooperative host phase its own cancellation allowance.
func cleanupCallback(ctx context.Context, timeout time.Duration, phase string, callback func(context.Context) error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return phaseError(phase, errors.Join(callHost(callback, cleanupCtx), context.Cause(cleanupCtx)))
}

func callPreparation(ctx context.Context, prepare PrepareAttemptFunc, info PreparedAttemptInfo) (lease *PreparedAttempt, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("preparation panic: %v\n%s", value, debug.Stack())
		}
	}()
	return prepare(ctx, info)
}

func callHost(callback func(context.Context) error, ctx context.Context) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("host callback panic: %v\n%s", value, debug.Stack())
		}
	}()
	if callback == nil {
		return errors.New("host callback is required")
	}
	return callback(ctx)
}

func phaseError(phase string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("eval: prepared %s: %w", phase, err)
}

func initialProbeStatus() *ProbeStatus {
	return &ProbeStatus{Execution: probeExecutionNotRun, Delivery: probeDeliveryNotDeclared, Evidence: probeStatusIncomplete, Quality: probeQualityNotAssessed}
}

// markProbeExecution precedes finalization so delivery errors do not rewrite execution.
func markProbeExecution(r *AttemptRecord, ctx context.Context) {
	if r.Probe == nil {
		return
	}
	r.Probe.Execution = probeExecutionSucceeded
	if r.HarnessErr != nil || r.Outcome == nil || r.Outcome.Err != nil {
		r.Probe.Execution = probeExecutionFailed
		if ctx.Err() != nil || errors.Is(r.HarnessErr, context.Canceled) || errors.Is(r.HarnessErr, context.DeadlineExceeded) || (r.Outcome != nil && (errors.Is(r.Outcome.Err, context.Canceled) || errors.Is(r.Outcome.Err, context.DeadlineExceeded))) {
			r.Probe.Execution = probeExecutionCanceled
		}
	}
}
