package runrecord

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

func (r *runReader) readCalls() error {
	found := make(map[runstore.CallKey]bool)
	for _, record := range r.callRecords {
		call, err := r.readCall(record)
		if err != nil {
			return fmt.Errorf("call %+v: %w", record.Key, err)
		}
		found[record.Key] = true
		r.run.Calls = append(r.run.Calls, call)
	}
	for key := range r.adopted {
		if !found[key] {
			return fmt.Errorf("runrecord: adopted call %+v is missing", key)
		}
	}
	return nil
}

func (r *runReader) readCall(record runstore.CallRecord) (Call, error) {
	if err := record.Validate(); err != nil {
		return Call{}, err
	}
	started, err := callrecord.DecodeStarted(record.Started)
	if err != nil {
		return Call{}, err
	}
	contextReader, err := r.callContextReader(started.Context, r.adopted[record.Key])
	if err != nil {
		return Call{}, err
	}
	if started.Key != record.Key || record.Key.RunID != r.run.RunID {
		return Call{}, fmt.Errorf("runrecord: started identity differs from record key")
	}
	call := Call{Record: record, Started: started, Activity: started.Activity, Step: started.Step, Class: Abandoned}
	if r.adopted[record.Key] {
		call.Class = Adopted
	} else if record.State == runstore.CallStarted {
		call.Class = Incomplete
	}
	call.Completion, err = r.readCompletion(record)
	if err != nil {
		return Call{}, err
	}

	// Rebuild reads request parts through a read-only collecting store so the
	// self-contained export preserves all referenced content, not just the view.
	store := collectingStore{Store: r.owner.store, reader: r.owner}
	call.Request, err = callrecord.Rebuild(r.owner.ctx, store, r.owner.record.RootRunID, started, func(_ctx context.Context, view index.ViewRef) ([]*model.Message, error) {
		if err := contextReader.resolveView(view); err != nil {
			return nil, err
		}
		return contextReader.project(view)
	})
	if err == nil && contextReader != r {
		r.collectCallCheckpoints(contextReader.run.Checkpoints)
	}
	return call, err
}

// collectCallCheckpoints preserves resolved evidence without sharing validation
// caches between calls whose captured context fingerprints can differ.
func (r *runReader) collectCallCheckpoints(checkpoints []Checkpoint) {
	for _, checkpoint := range checkpoints {
		if !slices.ContainsFunc(r.run.Checkpoints, func(existing Checkpoint) bool {
			return existing.Scope == checkpoint.Scope && existing.Hash == checkpoint.Hash
		}) {
			r.run.Checkpoints = append(r.run.Checkpoints, checkpoint)
		}
	}
}

// callContextReader verifies pre-adoption settings independently of run settings.
// An unfinished activity can record calls before any canonical adoption event.
// Different abandoned attempts must never establish the run's effective settings.
func (r *runReader) callContextReader(fingerprint string, adopted bool) (*runReader, error) {
	if r.run.ContextFingerprint != "" || adopted || fingerprint == "" {
		return r, r.checkContextFingerprint(fingerprint)
	}
	captured := &runReader{owner: r.owner, run: Run{RunID: r.run.RunID}, views: make(map[runstore.Hash][]*model.Message), checkpoints: make(map[string]bool)}
	if err := captured.recordContextFingerprint(fingerprint); err != nil {
		return nil, err
	}
	return captured, nil
}

func (r *runReader) readPresentations() error {
	for _, event := range r.run.Events {
		if event.Type != hooks.AssistantTurnCommitted {
			continue
		}
		var commit hooks.AssistantTurnCommittedEvent
		if err := json.Unmarshal(event.Payload, &commit); err != nil {
			return fmt.Errorf("decode presentation: %w", err)
		}
		if len(commit.CallKeys) != 0 && len(commit.CallKeys) != len(commit.PresentationIDs) {
			return fmt.Errorf("runrecord: presentation keys must parallel IDs")
		}
		for i, id := range commit.PresentationIDs {
			p := Presentation{ID: id, EventKey: event.EventKey, Class: PresentedUnrecorded}
			if len(commit.CallKeys) != 0 && commit.CallKeys[i] != nil {
				p.Call = commit.CallKeys[i]
				if r.overflowCalls[*p.Call] {
					return fmt.Errorf("runrecord: presentation committed from overflow attempt: %v", *p.Call)
				}
				p.Class = PresentedNotAdopted
				if r.adopted[*p.Call] {
					p.Class = PresentationAdopted
				}
			}
			r.run.Presentations = append(r.run.Presentations, p)
		}
	}
	return nil
}

func (r *runReader) readCompletion(record runstore.CallRecord) (*callrecord.Completion, error) {
	if record.State == runstore.CallStarted {
		return nil, nil
	}
	completion, err := callrecord.DecodeCompletion(record.Completion)
	if err != nil {
		return nil, err
	}
	if completion.Response != nil {
		if _, err := r.owner.source(r.owner.record.RootRunID)(r.owner.ctx, *completion.Response); err != nil {
			return nil, err
		}
	}
	return &completion, nil
}
