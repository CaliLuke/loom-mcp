package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// SealRecordActivityName returns the terminal sealing activity registered for an agent.
func SealRecordActivityName(id agent.Ident) string {
	return string(id) + ".seal"
}

// SealRecordActivity commits the remaining transcript before appending its seal.
// Fixed input identity and timestamp make retries, including lost acknowledgements,
// exact duplicates. Canonical append bypasses event interceptors and projections.
func (r *Runtime) SealRecordActivity(ctx context.Context, input *api.SealRecordInput) (*api.SealRecordOutput, error) {
	if err := validateSealRecordInput(input); err != nil {
		return nil, err
	}
	if r.RunStore == nil || r.RunEventStore == nil {
		return nil, fmt.Errorf("seal record: run store and run event store are required")
	}
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()
	// A fresh lineage validator reads at most the predecessor segment and stores
	// Pending through storeIndexSegments; sealing never depends on worker caches.
	cache := &runViewCache{segments: make(map[runstore.Hash]index.IndexSegment)}
	head, err := cache.commitPending(ctx, r.RunStore, input.RootRunID, func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return r.RunStore.Get(ctx, input.RootRunID, hash)
	}, index.ViewRef{IndexHead: input.IndexHead, Pending: input.Pending})
	if err != nil {
		return nil, fmt.Errorf("seal record index: %w", err)
	}
	sealed := sealedEvent(input, head)
	event, err := hooks.EncodeToHookInput(sealed, "")
	if err != nil {
		return nil, fmt.Errorf("encode run sealed: %w", err)
	}
	if _, err := r.RunEventStore.Append(ctx, &runlog.Event{
		RunID: input.RunID, EventKey: input.EventKey, Type: hooks.RunSealed, Payload: event.Payload, Timestamp: input.Timestamp.UTC(),
	}); err != nil {
		return nil, fmt.Errorf("append run sealed: %w", err)
	}
	if err := r.publishContinuation(ctx, input, head); err != nil {
		return nil, err
	}
	return &api.SealRecordOutput{IndexHead: head}, nil
}

// sealWorkflowRecord reads the same live state the plan loop owns, even on failure.
// Cancellation is detached before scheduling; the event timestamp is fixed once.
func (r *Runtime) sealWorkflowRecord(wfCtx engine.WorkflowContext, input *RunInput, base *workflowPlanInput) error {
	detached := wfCtx.Detached()
	if err := r.flushUnindexedResults(detached.Context(), base); err != nil {
		return fmt.Errorf("index terminal tool results: %w", err)
	}
	view := base.view()
	seal := &api.SealRecordInput{RootRunID: input.RootRunID, RunID: input.RunID, ContextCompaction: cloneCompaction(base.compaction), ContextRecovery: base.recovery, ContextSettings: base.contextSettings, ContextFingerprint: base.contextFingerprint, ProtocolVersion: base.protocolVersion, IndexHead: view.IndexHead, Pending: view.Pending, EventKey: "run_sealed:" + input.RunID, Timestamp: wfCtx.Now()}
	if base.Pos > 0 {
		setContinuationSealView(seal, view)
		seal.MinB, seal.SessionID, seal.Expected, seal.Pos = base.compactionState.MinB, input.SessionID, base.Prev, base.Pos
		seal.FinalHash, seal.Publish = base.FinalHash, base.PublishContinuation
	}
	out, err := detached.ExecuteSealRecordActivity(detached.Context(), engine.SealRecordActivityCall{Name: SealRecordActivityName(input.AgentID), Input: seal})
	if err != nil {
		return fmt.Errorf("seal workflow record: %w", err)
	}
	if out == nil {
		return fmt.Errorf("seal workflow record: activity returned no output")
	}
	base.adoptHead(out.IndexHead)
	return nil
}

func validateSealRecordInput(input *api.SealRecordInput) error {
	if input == nil || input.RootRunID == "" || input.RunID == "" {
		return fmt.Errorf("seal record: root run id and run id are required")
	}
	if input.EventKey != "run_sealed:"+input.RunID {
		return fmt.Errorf("seal record: fixed run_sealed event key is required")
	}
	if input.ProtocolVersion < 1 {
		return fmt.Errorf("seal record: protocol version is required")
	}
	return nil
}

func sealedEvent(input *api.SealRecordInput, head runstore.Hash) *hooks.RunSealedEvent {
	sealed := hooks.NewRunSealedEvent(input.RunID, head, input.ProtocolVersion, input.Timestamp)
	if input.Pos > 0 {
		sealed.FinalView = new(continuationSealView(input))
		if !input.Publish {
			sealed.ContinuationOutcome = "skipped"
		}
	}
	sealed.ContextFingerprint = input.ContextFingerprint
	sealed.ContextSettings = new(input.ContextSettings)
	sealed.RecoverySettings = new(input.ContextRecovery)
	if input.ContextCompaction != nil {
		settings := pureCompactionSettings(input.ContextCompaction)
		sealed.CompactionSettings = &settings
		sealed.KeepResultTools = cloneToolIdents(input.ContextCompaction.KeepResultTools)
	}
	return sealed
}

// continuationSealView reconstructs the final projection without duplicating index state.
func continuationSealView(input *api.SealRecordInput) index.ViewRef {
	return index.ViewRef{Checkpoint: input.Checkpoint, TailFrom: input.TailFrom, ClearedThrough: input.ClearedThrough, PreviewThrough: input.PreviewThrough, ForceClearThrough: input.ForceClearThrough, Pinned: input.Pinned, Budget: input.Budget, ChainPos: input.Pos, ContextFingerprint: input.ContextFingerprint}
}

func setContinuationSealView(input *api.SealRecordInput, view index.ViewRef) {
	input.Checkpoint, input.TailFrom, input.ClearedThrough = view.Checkpoint, view.TailFrom, view.ClearedThrough
	input.PreviewThrough, input.ForceClearThrough = view.PreviewThrough, view.ForceClearThrough
	input.Pinned, input.Budget = view.Pinned, view.Budget
}
