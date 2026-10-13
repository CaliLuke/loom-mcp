package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/continuation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

func continuationVisible(items [][]byte) ([]runstore.Hash, error) {
	var hashes []runstore.Hash
	for _, data := range items {
		var message model.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		hash, err := continuation.VisibleHash(&message)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, hash)
	}
	return hashes, nil
}

func (r *Runtime) recordContinuationStart(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	in := input.Continue
	saved, err := r.loadContinuationSeed(ctx, input.RootRunID, in.RunID)
	if err != nil || saved != nil {
		return saved, err
	}
	state := &api.ContinuationStartOutput{View: index.ViewRef{ContextFingerprint: in.ContextFingerprint}, Pos: 1, Outcome: "fresh", Reason: "no_head"}
	store, ok := r.SessionStore.(session.ContinuationStore)
	var head *session.Continuation
	if !ok {
		if !in.Settings.Default {
			return nil, fmt.Errorf("continuation requires ContinuationStore")
		}
		state.Reason = "store_unsupported"
	} else {
		head, err = store.LoadContinuationHead(ctx, in.SessionID)
		if err != nil {
			return nil, err
		}
	}
	state.Visible, err = continuationVisible(input.Items)
	if err != nil {
		return nil, err
	}

	out, err := r.tryContinuation(ctx, input, head, state)
	if err != nil {
		return nil, err
	}

	if out == nil {
		copy := *input
		copy.Continue = nil
		out, err = r.recordContentItems(ctx, &copy)
		if err != nil {
			return nil, err
		}
	}
	state.View.ChainPos = state.Pos
	out.Continuation = state
	return r.saveContinuationSeed(ctx, input, out)
}

func (r *Runtime) saveContinuationSeed(ctx context.Context, input *api.RecordContentInput, out *api.RecordContentOutput) (*api.RecordContentOutput, error) {
	in, state := input.Continue, out.Continuation
	data, err := json.Marshal(out, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	ref, err := r.RunStore.Put(ctx, input.RootRunID, data)
	if err != nil {
		return nil, err
	}
	event := continuationEvent{Prev: state.Prev, Pos: state.Pos, Outcome: state.Outcome, Reason: state.Reason, Seeded: len(out.Entries), SeedRef: ref}
	if err := r.appendContinuationEvent(ctx, in.RunID, in.SessionID, hooks.ContinuationStarted, in.Timestamp, event); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Runtime) loadContinuationSeed(ctx context.Context, scope, run string) (*api.RecordContentOutput, error) {
	event, err := r.continuationEvent(ctx, run, hooks.ContinuationStarted)
	if err != nil || event == nil {
		return nil, err
	}
	data, err := r.RunStore.Get(ctx, scope, event.SeedRef)
	if err != nil {
		return nil, err
	}
	var out api.RecordContentOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// seedContinuation validates every referenced blob before copying any carried
// bytes. Missing retained content permits a fresh start; store faults fail closed.
func (r *Runtime) seedContinuation(ctx context.Context, input *api.RecordContentInput, previous session.Continuation, state *api.ContinuationStartOutput) (*api.RecordContentOutput, error) {
	carried := append(append([]session.Carried{}, previous.Pins...), previous.Tail...)
	blobs, predecessor, summary, err := r.loadCarriedContent(ctx, previous, carried)
	if err != nil {
		return nil, err
	}

	entries, refs, err := r.copyContinuationEntries(ctx, input, previous, carried, blobs, state)
	if err != nil {
		return nil, err
	}

	head, committed, err := storeIndexSegments(ctx, r.RunStore, input.RootRunID, "", "", entries)
	if err != nil {
		return nil, err
	}
	state.View.IndexHead = head
	if previous.Checkpoint != nil {
		if err := r.seedCheckpoint(ctx, input, previous, predecessor, summary, blobs[len(carried):], committed, state); err != nil {
			return nil, err
		}
	}
	if err := r.seedContinuationBudget(ctx, input, previous, state); err != nil {
		return nil, err
	}
	return &api.RecordContentOutput{IndexHead: head, Entries: committed, Refs: refs}, nil
}

func (r *Runtime) seedCheckpoint(ctx context.Context, input *api.RecordContentInput, previous session.Continuation, predecessor index.CheckpointRecord, summary []byte, userBlobs [][]byte, entries []index.MessageEntry, state *api.ContinuationStartOutput) error {
	from := &index.RunCheckpoint{Scope: previous.ID, Run: previous.ID, Hash: *previous.Checkpoint}
	state.ContinuedFrom = from
	pins := len(previous.Pins)
	state.View.TailFrom = index.MessageID(pins + 1)
	for i := range pins {
		state.View.Pinned = append(state.View.Pinned, index.MessageID(i+1))
	}
	earlier, err := r.continuationEarlier(ctx, previous)
	if err != nil {
		return err
	}
	earlier.Users, err = r.seedUserIndex(ctx, input.RootRunID, previous.UserIndex, userBlobs)
	if err != nil {
		return err
	}

	copied := index.CheckpointRecord{From: from, Earlier: earlier, SummaryRun: predecessor.SummaryRun, SummaryPos: predecessor.SummaryPos, CitedUsers: predecessor.CitedUsers, Through: index.MessageID(pins), Fingerprint: index.Fingerprint(state.View.ContextFingerprint)}
	if pins > 0 {
		copied.Chain = entries[pins-1].Chain
	}
	if err := r.copyContinuationSummary(ctx, input.RootRunID, previous, predecessor, summary, &copied); err != nil {
		return err
	}

	data, err := json.Marshal(copied, json.Deterministic(true))
	if err != nil {
		return err
	}
	hash, err := r.RunStore.Put(ctx, input.RootRunID, data)
	if err != nil {
		return err
	}
	state.View.Checkpoint = &hash
	return nil
}

func (r *Runtime) seedContinuationBudget(ctx context.Context, input *api.RecordContentInput, previous session.Continuation, state *api.ContinuationStartOutput) error {
	// A planner can expose ContextInfo directly. Otherwise use the smallest
	// registered model budget: all registered clients are available to the planner.
	request := &model.Request{}
	r.mu.RLock()
	clients := make([]model.Client, 0, len(r.models))
	names := make([]string, 0, len(r.models))
	for name := range r.models {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		clients = append(clients, r.models[name])
	}
	r.mu.RUnlock()
	b := 0
	for _, client := range clients {
		info, ok := client.(model.ContextInfo)
		if !ok {
			continue
		}
		limits, _, known := info.ContextLimits(ctx, request)
		usable := limits.ContextWindow - contextmeasure.OutputReserve(request, limits)
		if known && usable > 0 && (b == 0 || usable < b) {
			b = usable
			state.Limits = limits
		}
	}
	if b == 0 {
		budget, err := r.continuationBudget(ctx, previous.ID)
		if err != nil {
			return err
		}
		if budget != (index.RenderBudget{}) {
			state.View.Budget = budget
			return nil
		}
		b = 60000
	}
	state.View.Budget = compaction.RenderBudget(input.Continue.Settings, b, 0)
	return nil
}

func (r *Runtime) loadCarriedContent(ctx context.Context, previous session.Continuation, carried []session.Carried) ([][]byte, index.CheckpointRecord, []byte, error) {
	all := append(append([]session.Carried{}, carried...), previous.UserIndex...)
	blobs := make([][]byte, len(all))
	for i, item := range all {
		if item.Bytes == nil {
			continue
		}
		scope := previous.ID
		if item.Entry.Origin != nil {
			scope = item.Entry.Origin.Scope
		}
		data, err := r.RunStore.Get(ctx, scope, *item.Bytes)
		if err != nil {
			return nil, index.CheckpointRecord{}, nil, err
		}
		blobs[i] = data
	}
	var predecessor index.CheckpointRecord
	var summary []byte
	if previous.Checkpoint != nil {
		data, err := r.RunStore.Get(ctx, previous.ID, *previous.Checkpoint)
		if err != nil {
			return nil, index.CheckpointRecord{}, nil, err
		}
		if err := json.Unmarshal(data, &predecessor); err != nil {
			return nil, index.CheckpointRecord{}, nil, err
		}
		if predecessor.Summary != nil {
			summary, err = r.RunStore.Get(ctx, previous.ID, *predecessor.Summary)
			if err != nil {
				return nil, index.CheckpointRecord{}, nil, err
			}
		}
	}
	return blobs, predecessor, summary, nil
}

func (r *Runtime) seedUserIndex(ctx context.Context, scope string, items []session.Carried, userBlobs [][]byte) ([]index.IndexLine, error) {
	var lines []index.IndexLine
	for i, item := range items {
		if item.Bytes == nil {
			continue
		}
		data := userBlobs[i]
		if _, err := r.RunStore.Put(ctx, scope, data); err != nil {
			return nil, err
		}
		var message model.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, err
		}
		text := ""
		for _, part := range message.Parts {
			if p, ok := part.(model.TextPart); ok {
				text += p.Text
			}
		}
		lines = append(lines, index.IndexLine{ID: item.Entry.ID, Origin: item.Entry.Origin, Text: text})
	}
	return lines, nil
}

func (r *Runtime) tryContinuation(ctx context.Context, input *api.RecordContentInput, head *session.Continuation, state *api.ContinuationStartOutput) (*api.RecordContentOutput, error) {
	var out *api.RecordContentOutput
	var err error
	if head != nil {
		state.Prev = &head.ID
		state.Pos = head.Pos + 1
		state.Reason = "no_match"
		if continuation.Match(head.Visible, state.Visible) {
			out, err = r.seedContinuation(ctx, input, *head, state)
			if err != nil {
				if !errors.Is(err, runstore.ErrNotFound) {
					return nil, err
				}
				err = nil
				state.Reason = "missing_content"
				state.View = index.ViewRef{ContextFingerprint: input.Continue.ContextFingerprint}
				state.ContinuedFrom = nil
			} else {
				state.Outcome = "continued"
				state.Reason = ""
			}
		}
	}
	return out, err
}

func (r *Runtime) copyContinuationEntries(ctx context.Context, input *api.RecordContentInput, previous session.Continuation, carried []session.Carried, blobs [][]byte, state *api.ContinuationStartOutput) ([]index.MessageEntry, []runstore.Hash, error) {
	entries := make([]index.MessageEntry, 0, len(carried)+len(input.Items))
	batches := make(map[index.MessageID]index.MessageID)
	for i, item := range carried {
		entry := item.Entry
		entry.ID = index.MessageID(len(entries) + 1)
		entry.Chain = ""
		if entry.Batch != 0 {
			if batches[entry.Batch] == 0 {
				batches[entry.Batch] = entry.ID
			}
			entry.Batch = batches[entry.Batch]
		}
		if item.Bytes != nil {
			hash, err := r.RunStore.Put(ctx, input.RootRunID, blobs[i])
			if err != nil {
				return nil, nil, err
			}
			entry.Hash = hash
		}
		if entry.Origin != nil && entry.Origin.Form != continuation.Full {
			state.View.CarriedPartial = true
		}
		entries = append(entries, entry)
	}
	return r.appendContinuationInput(ctx, input.RootRunID, entries, input.Items[len(previous.Visible):])
}

func (r *Runtime) appendContinuationInput(ctx context.Context, scope string, entries []index.MessageEntry, items [][]byte) ([]index.MessageEntry, []runstore.Hash, error) {
	var refs []runstore.Hash
	for _, data := range items {
		hash, err := r.RunStore.Put(ctx, scope, data)
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, hash)
		var msg model.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, nil, err
		}
		size, err := contextmeasure.MeasureMessages([]*model.Message{&msg})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, index.MessageEntry{ID: index.MessageID(len(entries) + 1), Kind: index.Message, Hash: hash, Role: msg.Role, Bytes: size.Bytes, NonText: size.NonText})
	}
	return entries, refs, nil
}

func (r *Runtime) copyContinuationSummary(ctx context.Context, scope string, previous session.Continuation, predecessor index.CheckpointRecord, summary []byte, copied *index.CheckpointRecord) error {
	for i := range copied.CitedUsers {
		if copied.CitedUsers[i].Origin == nil {
			copied.CitedUsers[i].Origin = &index.Origin{Scope: previous.ID, Run: previous.ID, Pos: previous.Pos, ID: copied.CitedUsers[i].ID, Form: continuation.Full}
		}
	}
	if predecessor.Summary != nil {
		hash, err := r.RunStore.Put(ctx, scope, summary)
		if err != nil {
			return err
		}
		copied.Summary = &hash
	}
	return nil
}
