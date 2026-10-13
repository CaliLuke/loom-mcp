package runrecord

import (
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextcompat"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

func (r *runReader) readCompatibility(event *runlog.Event) error {
	var adoption hooks.ContextCompactionEvent
	if err := json.Unmarshal(event.Payload, &adoption); err != nil {
		return err
	}
	if adoption.Compatibility == nil || adoption.Step <= r.lastPlannerStep || adoption.Step <= r.lastCompatibilityStep || r.state.Flags.NonTextResident || r.pendingResidency != nil {
		return fmt.Errorf("compatibility adoption is missing or out of order")
	}
	store := collectingStore{Store: r.owner.store, reader: r.owner}
	fact, err := contextcompat.ReadFact(r.owner.ctx, store, r.owner.record.RootRunID, *adoption.Compatibility)
	if err != nil {
		return err
	}
	request := fact.Request
	if request.RunID != r.run.RunID || request.Support != compaction.SupportFingerprint(r.state) {
		return fmt.Errorf("compatibility adoption support or run mismatch")
	}
	expected := contextcompat.BuildRequest(request.Scope, request.RunID, request.IndexHead, request.Pending, nil, r.state)
	if !slices.Equal(request.Calls, expected.Calls) {
		return fmt.Errorf("compatibility adoption must use the first observed calls")
	}
	for _, key := range request.Calls {
		if !r.adopted[key] {
			return fmt.Errorf("compatibility call was not adopted: %+v", key)
		}
	}
	if err := contextcompat.Validate(r.owner.ctx, store, request, *adoption.Compatibility); err != nil {
		return err
	}
	entries, _, err := contextcompat.Resolve(r.owner.ctx, store, request)
	if err != nil {
		return err
	}
	if err := r.verifyCompatibilityPrefix(entries); err != nil {
		return err
	}
	r.state.Compatibility = adoption.Compatibility.Evidence
	r.lastCompatibilityStep = adoption.Step
	return nil
}

func (r *runReader) verifyCompatibilityPrefix(entries []index.MessageEntry) error {
	previous, err := index.Walk(r.owner.ctx, r.owner.source(r.owner.record.RootRunID), r.run.IndexHead)
	if err != nil {
		return err
	}
	if len(entries) < len(previous) {
		return fmt.Errorf("compatibility coverage omits adopted entries")
	}
	var chain runstore.Hash
	for i, entry := range previous {
		actual, err := index.ComputeChain(chain, entries[i])
		if err != nil {
			return err
		}
		if actual != entry.Chain {
			return fmt.Errorf("compatibility coverage rewrites adopted entry %d", entry.ID)
		}
		chain = actual
	}
	return nil
}

// verifyNonTextBoundary reconstructs residency from prior adopted facts and
// protected compatibility evidence at the affected planner decision. Ordinary
// residency diagnostics may be dropped or rewritten by application hooks.
func (r *runReader) verifyNonTextBoundary(head runstore.Hash, step int, decision bool) error {
	if err := r.verifyResidencyAdoption(head, step); err != nil {
		return err
	}
	settings := r.run.ContextSettings
	if settings == nil || !settings.KeepNonTextResident || !decision || len(r.state.Models) == 0 || !compaction.On(r.state, nil, *settings) {
		return nil
	}
	entries, err := index.Walk(r.owner.ctx, r.owner.source(r.owner.record.RootRunID), head)
	if err != nil {
		return err
	}
	originals := false
	for _, entry := range entries {
		if entry.NonText > 0 {
			originals = true
			break
		}
	}
	if !originals {
		return nil
	}
	if resident, _, _ := compaction.NonTextResident(r.state, entries, *settings); resident {
		return fmt.Errorf("non-text residency adoption missing before planner")
	}
	evidence := r.state.Compatibility
	if !evidence.Checked || !evidence.Compatible || evidence.Coverage != compaction.NonTextCoverage(entries) || evidence.Support != compaction.SupportFingerprint(r.state) {
		return fmt.Errorf("current attachment compatibility missing before planner")
	}
	return nil
}
