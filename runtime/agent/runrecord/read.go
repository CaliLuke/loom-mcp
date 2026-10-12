package runrecord

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

type reader struct {
	ctx      context.Context
	store    runstore.Store
	log      runlog.Store
	record   *RunRecord
	resolved map[string]bool
}

type runReader struct {
	owner         *reader
	run           Run
	adopted       map[runstore.CallKey]bool
	renderer      index.Renderer
	rendered      []index.MessageEntry
	views         map[runstore.Hash][]*model.Message
	checkpoints   map[string]bool
	state         compaction.State
	overflowCalls map[runstore.CallKey]bool
	callRecords   []runstore.CallRecord
	byKey         map[runstore.CallKey]runstore.CallRecord
}

// Read reconstructs a parent-first run tree from root-scoped content and the
// canonical log alone. It never queries a workflow engine or observer projection.
func Read(ctx context.Context, store runstore.Store, log runlog.Store, rootRunID, runID string) (*RunRecord, error) {
	if store == nil || log == nil || rootRunID == "" || runID == "" {
		return nil, fmt.Errorf("runrecord: stores, root_run_id and run_id are required")
	}
	r := reader{ctx: ctx, store: store, log: log, record: &RunRecord{RootRunID: rootRunID, Content: make(map[runstore.Hash][]byte)}, resolved: make(map[string]bool)}
	if err := r.readTree(runID, make(map[string]bool)); err != nil {
		return nil, fmt.Errorf("read runrecord %s: %w", runID, err)
	}
	return r.record, nil
}

func (r *reader) readTree(runID string, seen map[string]bool) error {
	if seen[runID] {
		return nil
	}
	seen[runID] = true
	rr := runReader{owner: r, run: Run{RunID: runID}, adopted: make(map[runstore.CallKey]bool), views: make(map[runstore.Hash][]*model.Message), checkpoints: make(map[string]bool), overflowCalls: make(map[runstore.CallKey]bool)}
	calls, err := r.store.ListCalls(r.ctx, r.record.RootRunID, runID)
	if err != nil {
		return fmt.Errorf("list calls: %w", err)
	}
	rr.callRecords = calls
	rr.byKey = make(map[runstore.CallKey]runstore.CallRecord, len(calls))
	for _, call := range calls {
		rr.byKey[call.Key] = call
	}
	children, err := rr.readEvents()
	if err != nil {
		return err
	}
	rr.run.Entries, err = index.Walk(r.ctx, r.source(r.record.RootRunID), rr.run.IndexHead)
	if err != nil {
		return err
	}
	rr.run.Transcript, err = rr.render(rr.run.IndexHead)
	if err != nil {
		return err
	}
	if err := rr.readCalls(); err != nil {
		return err
	}
	if err := rr.readEntryContent(); err != nil {
		return err
	}
	if err := rr.readPresentations(); err != nil {
		return err
	}
	r.record.Runs = append(r.record.Runs, rr.run)
	for _, child := range children {
		if err := r.readTree(child, seen); err != nil {
			return err
		}
	}
	return nil
}

func (r *runReader) readEvents() ([]string, error) {
	var children []string
	cursor := ""
	for {
		page, err := r.owner.log.List(r.owner.ctx, r.run.RunID, cursor, 100)
		if err != nil {
			return nil, fmt.Errorf("list events for %s: %w", r.run.RunID, err)
		}
		for _, event := range page.Events {
			if event == nil || event.RunID != r.run.RunID {
				return nil, fmt.Errorf("runrecord: invalid event run identity")
			}
			r.run.Events = append(r.run.Events, event)
			child, err := r.readEvent(event)
			if err != nil {
				return nil, fmt.Errorf("event %s: %w", event.EventKey, err)
			}
			if child != "" {
				children = append(children, child)
			}
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			return nil, fmt.Errorf("runrecord: event pagination did not advance")
		}
		cursor = page.NextCursor
	}
	return children, nil
}

func (r *runReader) readEvent(event *runlog.Event) (string, error) {
	if event.Type == hooks.ContinuationStarted {
		var start struct {
			Pos  int     `json:"pos"`
			Prev *string `json:"prev"`
		}
		if err := json.Unmarshal(event.Payload, &start); err != nil {
			return "", err
		}
		r.run.Pos, r.run.Prev = start.Pos, start.Prev
		return "", nil
	}
	if event.Type == hooks.ChildRunLinked {
		var link hooks.ChildRunLinkedEvent
		if err := json.Unmarshal(event.Payload, &link); err != nil {
			return "", err
		}
		if link.ChildRunID == "" {
			return "", fmt.Errorf("runrecord: child link lacks run ID")
		}
		return link.ChildRunID, nil
	}
	if event.Type == hooks.ContextOverflow {
		return "", r.readOverflow(event)
	}
	if event.Type == hooks.PlannerStep {
		return "", r.readStep(event)
	}
	if event.Type == hooks.ContextRecalled {
		return "", r.readRecallCharge(event)
	}

	if event.Type == hooks.RunSealed {
		return "", r.readSeal(event)
	}
	return "", r.readCompactionRecord(event)
}

func (r *runReader) adopt(keys []runstore.CallKey) error {
	for _, key := range keys {
		if key.RunID != r.run.RunID || key.Seq < 1 || key.Attempt < 1 {
			return fmt.Errorf("runrecord: invalid adopted key %+v", key)
		}
		r.adopted[key] = true
	}
	return nil
}

// render extends one incremental renderer along consecutive heads. An orphan
// attempt's different lineage resets that same renderer; snapshots keep earlier
// recorded views available without ever substituting the current transcript.
func (r *runReader) render(head runstore.Hash) ([]*model.Message, error) {
	if view, ok := r.views[head]; ok {
		return view, nil
	}
	entries, err := index.Walk(r.owner.ctx, r.owner.source(r.owner.record.RootRunID), head)
	if err != nil {
		return nil, err
	}
	common := len(r.rendered)
	if common > len(entries) || common > 0 && r.rendered[common-1].Chain != entries[common-1].Chain {
		r.renderer = index.Renderer{}
		common = 0
	}
	if err := r.renderer.Append(r.owner.ctx, entries[common:], r.owner.source(r.owner.record.RootRunID)); err != nil {
		return nil, err
	}
	messages, err := r.renderer.Messages()
	if err != nil {
		return nil, err
	}
	r.rendered = entries
	r.views[head] = messages
	return messages, nil
}

func (r *runReader) resolveView(view index.ViewRef) error {
	if len(view.Pending) != 0 {
		return fmt.Errorf("runrecord: recorded view must be committed")
	}
	if view.Checkpoint != nil {
		if err := r.checkpoint(r.owner.record.RootRunID, *view.Checkpoint, make(map[string]bool)); err != nil {
			return err
		}
	}
	_, err := r.render(view.IndexHead)
	return err
}

func (r *reader) source(scope string) index.Source {
	return func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		key := scope + "\x00" + string(hash)
		if r.resolved[key] {
			return r.record.Content[hash], nil
		}
		data, err := r.store.Get(ctx, scope, hash)
		if err != nil {
			return nil, fmt.Errorf("resolve %s in %s: %w", hash, scope, err)
		}
		r.record.Content[hash] = data
		r.resolved[key] = true
		return data, nil
	}
}

func (r *runReader) readStep(event *runlog.Event) error {
	var step hooks.PlannerStepEvent
	if err := json.Unmarshal(event.Payload, &step); err != nil {
		return err
	}
	if step.Step < 1 || step.IndexHead != step.ViewRef.IndexHead || len(step.ViewRef.Pending) != 0 {
		return fmt.Errorf("runrecord: invalid committed planner view")
	}
	if err := r.recordProtocolVersion(step.ProtocolVersion); err != nil {
		return err
	}
	if err := r.recordCompactionSettings(step.CompactionSettings, step.KeepResultTools); err != nil {
		return err
	}
	if err := r.recordRecoverySettings(step.RecoverySettings); err != nil {
		return err
	}
	if err := r.recordContextFingerprint(step.ContextFingerprint); err != nil {
		return err
	}
	if err := r.recordContextSettings(step.ContextSettings); err != nil {
		return err
	}
	if !r.run.Sealed {
		r.run.IndexHead = step.IndexHead
	}
	if err := r.adopt(step.CallKeys); err != nil {
		return err
	}
	r.state.ValidateBudgets = step.ContextOverrides
	if err := r.observeStepFacts(step.CallKeys); err != nil {
		return err
	}
	return r.resolveView(step.ViewRef)
}

// recordProtocolVersion derives the run version only from explicit record
// stamps. Calls and event kinds cannot identify a workflow protocol.
func (r *runReader) recordProtocolVersion(version int) error {
	if version < 1 {
		return fmt.Errorf("runrecord: record event protocol version is required")
	}
	if r.run.WorkflowProtocolVersion != 0 && r.run.WorkflowProtocolVersion != version {
		return fmt.Errorf("runrecord: conflicting workflow protocol versions %d and %d", r.run.WorkflowProtocolVersion, version)
	}
	r.run.WorkflowProtocolVersion = version
	return nil
}

// project reconstructs the exact request view, including bounded checkpoint and cleared results.
func (r *runReader) project(view index.ViewRef) ([]*model.Message, error) {
	if view.Checkpoint == nil && view.TailFrom <= 1 && view.ClearedThrough == 0 && view.PreviewThrough == 0 && view.ForceClearThrough == 0 {
		return r.render(view.IndexHead)
	}
	source := r.owner.source(r.owner.record.RootRunID)
	entries, err := index.Walk(r.owner.ctx, source, view.IndexHead)
	if err != nil {
		return nil, err
	}
	return checkpoint.Project(r.owner.ctx, source, entries, view)
}

func (r *runReader) recordCompactionSettings(settings *compaction.Settings, keep []tools.Ident) error {
	if settings == nil {
		return nil
	}
	if r.run.CompactionSettings != nil && (*r.run.CompactionSettings != *settings || !slices.Equal(r.run.KeepResultTools, keep)) {
		return fmt.Errorf("conflicting recorded compaction settings")
	}
	if r.run.CompactionSettings == nil {
		r.run.Recall = compaction.NewRecall(*settings)
	}
	r.run.CompactionSettings = settings
	r.run.KeepResultTools = slices.Clone(keep)
	return nil
}

func (r *runReader) readSeal(event *runlog.Event) error {
	var seal hooks.RunSealedEvent
	if err := json.Unmarshal(event.Payload, &seal); err != nil {
		return err
	}
	if err := r.recordProtocolVersion(seal.ProtocolVersion); err != nil {
		return err
	}
	if err := r.recordCompactionSettings(seal.CompactionSettings, seal.KeepResultTools); err != nil {
		return err
	}
	if err := r.recordRecoverySettings(seal.RecoverySettings); err != nil {
		return err
	}
	if err := r.recordContextFingerprint(seal.ContextFingerprint); err != nil {
		return err
	}
	if err := r.recordContextSettings(seal.ContextSettings); err != nil {
		return err
	}
	r.run.Sealed = true
	r.run.IndexHead = seal.IndexHead
	_, err := r.render(seal.IndexHead)
	return err
}

func (r *runReader) readRecallCharge(event *runlog.Event) error {
	var charge hooks.ContextRecalledEvent
	if err := json.Unmarshal(event.Payload, &charge); err != nil {
		return err
	}
	expected := r.run.Recall
	accepted := expected.Charge(charge.Kind == recall.Read)
	if accepted == (charge.Outcome == "exhausted") || expected.Discovery != charge.RemainingDiscovery || expected.Reads != charge.RemainingReads {
		return fmt.Errorf("runrecord: inconsistent recall charge")
	}
	r.run.Recall = expected
	return nil
}

func (r *runReader) recordRecoverySettings(settings *run.ContextRecovery) error {
	if settings == nil {
		return nil
	}
	if r.run.RecoverySettings != nil && *r.run.RecoverySettings != *settings {
		return fmt.Errorf("conflicting recorded recovery settings")
	}
	r.run.RecoverySettings = settings
	return nil
}

func (r *runReader) recordContextSettings(settings *compaction.ContextSettings) error {
	if settings == nil {
		return nil
	}
	if r.run.ContextSettings == nil {
		r.run.Recall = compaction.NewRecall(*settings)
	} else if *r.run.ContextSettings != *settings {
		return fmt.Errorf("conflicting recorded context settings")
	}
	r.run.ContextSettings = settings
	return nil
}

func (r *runReader) observeStepFacts(keys []runstore.CallKey) error {
	var facts []contextmeasure.RequestFacts
	for _, key := range keys {
		record, ok := r.byKey[key]
		if !ok {
			return fmt.Errorf("runrecord: adopted call %v is missing", key)
		}
		if record.State == runstore.CallStarted {
			continue
		}
		completion, err := callrecord.DecodeCompletion(record.Completion)
		if err != nil {
			return err
		}
		if completion.Facts != nil {
			facts = append(facts, *completion.Facts)
		}
	}
	if r.run.ContextFingerprint != "" && r.run.ContextSettings != nil {
		var err error
		r.state, err = compaction.ObserveEffective(r.state, facts, *r.run.ContextSettings)
		if err != nil {
			return err
		}
	} else {
		r.state = compaction.Observe(r.state, facts)
	}
	r.run.ToolResultParts = compaction.ToolResultParts(r.state)
	return nil
}

func (r *runReader) readCompactionRecord(event *runlog.Event) error {
	if event.Type == hooks.ContextCompacted || event.Type == hooks.ContextCompactionSkipped {
		var summary hooks.ContextCompactionEvent
		if err := json.Unmarshal(event.Payload, &summary); err != nil {
			return err
		}
		if err := r.recordContextFingerprint(summary.Fingerprint); err != nil {
			return err
		}
		if err := r.recordContextSettings(summary.ContextSettings); err != nil {
			return err
		}
		if event.Type == hooks.ContextCompactionSkipped {
			r.run.Summary = r.run.Summary.Reject(summary.Kind)
		}
		if summary.ViewRef != nil && !r.run.Sealed {
			r.run.IndexHead = summary.ViewRef.IndexHead
		}
		if event.Type == hooks.ContextCompacted {
			settings := compaction.Settings{}
			if r.run.ContextSettings != nil {
				settings = *r.run.ContextSettings
			} else if r.run.CompactionSettings != nil {
				settings = *r.run.CompactionSettings
			}
			r.run.Recall = r.run.Recall.Refilled(settings)
		}
		// The common envelope reads canonical compaction keys and retained K8
		// exports. K12 produces an empty call_keys list.
		var adoption struct {
			CallKeys []runstore.CallKey `json:"call_keys,case:ignore"`
			ViewRef  *index.ViewRef     `json:"view_ref,case:ignore"`
		}
		if err := json.Unmarshal(event.Payload, &adoption); err != nil {
			return err
		}
		if err := r.adopt(adoption.CallKeys); err != nil {
			return err
		}
		if adoption.ViewRef != nil {
			return r.resolveView(*adoption.ViewRef)
		}
	}
	return nil
}
