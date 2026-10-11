package runrecord

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type reader struct {
	ctx      context.Context
	store    runstore.Store
	log      runlog.Store
	record   *RunRecord
	resolved map[string]bool
}

type runReader struct {
	owner       *reader
	run         Run
	adopted     map[runstore.CallKey]bool
	renderer    index.Renderer
	rendered    []index.MessageEntry
	views       map[runstore.Hash][]*model.Message
	checkpoints map[string]bool
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
	rr := runReader{owner: r, run: Run{RunID: runID}, adopted: make(map[runstore.CallKey]bool), views: make(map[runstore.Hash][]*model.Message), checkpoints: make(map[string]bool)}
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
	if event.Type == hooks.PlannerStep {
		return "", r.readStep(event)
	}
	if event.Type == hooks.RunSealed {
		var seal hooks.RunSealedEvent
		if err := json.Unmarshal(event.Payload, &seal); err != nil {
			return "", err
		}
		if err := r.recordProtocolVersion(seal.ProtocolVersion); err != nil {
			return "", err
		}
		r.run.Sealed = true
		r.run.IndexHead = seal.IndexHead
		_, err := r.render(seal.IndexHead)
		return "", err
	}
	if event.Type == hooks.ContextCompacted || event.Type == hooks.ContextCompactionSkipped || event.Type == hooks.ContextOverflow {
		// Reserved record events have no K8 producer. The common adoption envelope
		// lets a fake summarize activity exercise all returned chunk keys.
		var adoption struct {
			CallKeys []runstore.CallKey
			ViewRef  *index.ViewRef
		}
		if err := json.Unmarshal(event.Payload, &adoption); err != nil {
			return "", err
		}
		if err := r.adopt(adoption.CallKeys); err != nil {
			return "", err
		}
		if adoption.ViewRef != nil {
			return "", r.resolveView(*adoption.ViewRef)
		}
	}
	return "", nil
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
	if !r.run.Sealed {
		r.run.IndexHead = step.IndexHead
	}
	if err := r.adopt(step.CallKeys); err != nil {
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
