package runrecord

import (
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// readResidencyAdoption accepts only an actual next preparation boundary. Its
// pending original entries are checked when the planner commits them or the run seals.
func (r *runReader) readResidencyAdoption(event *runlog.Event) error {
	var adoption hooks.ContextResidencyEvent
	if err := json.Unmarshal(event.Payload, &adoption); err != nil {
		return err
	}
	settings := r.run.ContextSettings
	if r.run.Sealed || r.pendingResidency != nil || r.state.Flags.NonTextResident || r.overflowRetry || adoption.Step != r.lastPlannerStep+1 {
		return fmt.Errorf("residency adoption is duplicate or out of order")
	}
	if settings == nil || !settings.KeepNonTextResident || len(r.state.Models) == 0 || !compaction.On(r.state, nil, *settings) {
		return fmt.Errorf("residency adoption has no eligible observed facts")
	}
	if adoption.RootRunID != r.owner.record.RootRunID || adoption.Coverage == "" || adoption.Support != compaction.SupportFingerprint(r.state) {
		return fmt.Errorf("residency adoption scope, coverage or support mismatch")
	}
	if adoption.Reason != "provider" && adoption.Reason != "too_large" || adoption.Model == "" {
		return fmt.Errorf("invalid residency adoption witness")
	}
	r.pendingResidency = &adoption
	return nil
}

// verifyResidencyAdoption never infers a new decision merely from a terminal
// view. A protected preparation record must already exist, even for failed calls.
func (r *runReader) verifyResidencyAdoption(head runstore.Hash, step int) error {
	adoption := r.pendingResidency
	if adoption == nil {
		return nil
	}
	entries, err := index.Walk(r.owner.ctx, r.owner.source(r.owner.record.RootRunID), head)
	if err != nil {
		return err
	}
	if step != 0 && step != adoption.Step {
		finalizer, err := r.residencyFinalizerTransition(entries, step)
		if err != nil {
			return err
		}
		if !finalizer {
			return fmt.Errorf("residency adoption planner step mismatch")
		}
	}
	if adoption.Coverage != compaction.NonTextCoverage(entries) || adoption.Support != compaction.SupportFingerprint(r.state) {
		return fmt.Errorf("residency adoption differs from committed coverage or support")
	}
	resident, reason, witness := compaction.NonTextResident(r.state, entries, *r.run.ContextSettings)
	if !resident || reason != adoption.Reason || witness != adoption.Model {
		return fmt.Errorf("residency adoption differs from recorded decision evidence")
	}
	r.state.Flags.NonTextResident = true
	r.run.NonTextResident = true
	r.pendingResidency = nil
	return nil
}

// residencyFinalizerTransition recognizes only the immediate grace finalizer
// after an expired prepared schedule. Its runtime-owned structural pair must be
// newly appended after the previous adopted prefix, not inherited from an old view.
func (r *runReader) residencyFinalizerTransition(entries []index.MessageEntry, step int) (bool, error) {
	if step != r.pendingResidency.Step+1 || len(entries) < 2 {
		return false, nil
	}
	results, hint := entries[len(entries)-2], entries[len(entries)-1]
	if results.Kind != index.Structural || results.Structural == nil || results.Structural.Kind != index.FinalizeResults || hint.Kind != index.Structural || hint.Structural == nil || hint.Structural.Kind != index.FinalizeHint || hint.Structural.Reason != string(planner.TerminationReasonTimeBudget) {
		return false, nil
	}
	previous, err := index.Walk(r.owner.ctx, r.owner.source(r.owner.record.RootRunID), r.run.IndexHead)
	if err != nil {
		return false, err
	}
	if len(entries) < len(previous)+2 {
		return false, nil
	}
	if len(previous) > 0 && entries[len(previous)-1].Chain != previous[len(previous)-1].Chain {
		return false, nil
	}
	return true, nil
}
