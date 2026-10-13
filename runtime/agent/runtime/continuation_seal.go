package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/continuation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// carryEntries materializes only the final rendered form. Server-only evidence
// and original results behind cleared placeholders are never loaded.
func carryEntries(ctx context.Context, source index.Source, entries []index.MessageEntry, view index.ViewRef) ([]continuation.Entry, error) {
	cleared, err := checkpoint.ResultText(ctx, source, entries, view)
	if err != nil {
		return nil, err
	}
	hidden := hiddenCalls(entries)
	out := make([]continuation.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.ID < max(view.TailFrom, 1) {
			continue
		}
		messages, err := index.RenderTail(ctx, []index.MessageEntry{entry}, source, hidden, cleared)
		if err != nil {
			return nil, err
		}
		messages = removeHiddenCarry(messages, hidden)
		entry.ToolUseIDs = slices.DeleteFunc(slices.Clone(entry.ToolUseIDs), func(id string) bool {
			return hidden[id]
		})
		form := continuation.Full
		if entry.Kind == index.ToolResult && entry.OmitsResultContent() {
			form = continuation.Preview
		}
		if entry.Origin != nil {
			form = entry.Origin.Form
		}
		if _, ok := cleared[entry.ID]; ok {
			form = continuation.Preview
			if entry.ID <= view.ForceClearThrough || (entry.ID <= view.ClearedThrough && !entry.Keep) {
				form = continuation.Placeholder
			}
		}
		out = append(out, continuation.Entry{Entry: entry, Messages: messages, Form: form})
	}
	return out, nil
}

func continuationPlan(ctx context.Context, source index.Source, entries []index.MessageEntry, in *api.SealRecordInput) (continuation.Plan, error) {
	rendered, err := carryEntries(ctx, source, entries, continuationSealView(in))
	if err != nil {
		return continuation.Plan{}, err
	}
	for i := range rendered {
		if rendered[i].Entry.Origin == nil {
			rendered[i].Entry.Origin = &index.Origin{Scope: in.RootRunID, Run: in.RunID, Pos: in.Pos, ID: rendered[i].Entry.ID, Form: rendered[i].Form}
		}
	}
	plan, err := continuation.Carry(rendered, continuationSealView(in), continuation.Cap(in.ContextSettings, in.MinB), 256)
	if err != nil {
		return continuation.Plan{}, err
	}
	return plan, nil
}

// removeHiddenCarry applies final bookkeeping to independently rendered entries.
func removeHiddenCarry(messages []*model.Message, hidden map[string]bool) []*model.Message {
	for _, message := range messages {
		message.Parts = slices.DeleteFunc(message.Parts, func(part model.Part) bool {
			call, ok := part.(model.ToolUsePart)
			return ok && hidden[call.ID]
		})
	}
	return slices.DeleteFunc(messages, func(message *model.Message) bool {
		return len(message.Parts) == 0
	})
}
func (r *Runtime) publishContinuation(ctx context.Context, in *api.SealRecordInput, head runstore.Hash) error {
	if !in.Publish {
		return nil
	}
	store, ok := r.SessionStore.(session.ContinuationStore)
	if !ok {
		if in.ContextSettings.Default {
			return nil
		}
		return fmt.Errorf("continuation requires ContinuationStore")
	}
	inputHash, done, err := r.continuationPublishRetry(ctx, in)
	if err != nil || done {
		return err
	}
	next, err := r.buildContinuation(ctx, in, head)
	if err != nil {
		return err
	}

	outcome, err := store.AdvanceContinuation(ctx, in.SessionID, in.Expected, next)
	if err != nil {
		return err
	}
	event := continuationEvent{InputHash: inputHash, ID: in.RunID, Prev: in.Expected, Pos: in.Pos, Outcome: string(outcome)}
	kind := hooks.ContinuationPublished
	switch outcome {
	case session.Conflict:
		return engine.NewNonRetryableApplicationError("continuation body conflict", "continuation_conflict", nil, nil)
	case session.Superseded:
		kind = hooks.ContinuationSuperseded
		current, err := store.LoadContinuationHead(ctx, in.SessionID)
		if err != nil {
			return err
		}
		if current != nil {
			event.Head = &current.ID
		}
	case session.Published, session.AlreadyPublished:
	}
	return r.appendContinuationEvent(ctx, in.RunID, in.SessionID, kind, in.Timestamp, event)
}

func (r *Runtime) storeCarried(ctx context.Context, in *api.SealRecordInput, carried continuation.Entry) (session.Carried, error) {
	old := carried.Entry
	origin := old.Origin
	if origin == nil {
		origin = &index.Origin{Scope: in.RootRunID, Run: in.RunID, Pos: in.Pos, ID: old.ID, Form: carried.Form}
	} else {
		copy := *origin
		copy.Form = carried.Form
		origin = &copy
	}
	entry := index.MessageEntry{ID: old.ID, Kind: index.Assistant, Role: old.Role, Origin: origin, Keep: old.Keep, ToolUseIDs: old.ToolUseIDs, Batch: old.Batch, Bytes: old.Bytes}
	// A carried result has its own structural encoding and cannot recover an old
	// body through Keep or inherited watermarks.
	if carried.Form == continuation.Placeholder && len(carried.Messages) == 1 && len(carried.Messages[0].Parts) == 1 {
		if result, ok := carried.Messages[0].Parts[0].(model.ToolResultPart); ok {
			entry.Kind = index.Structural
			entry.Structural = &index.StructuralEntry{Kind: index.CarriedResult, ProtocolVersion: index.TemplateProtocolVersion, Text: []index.TextParameter{{Text: fmt.Sprint(result.Content)}}}
			if result.IsError {
				entry.Result = &index.ResultProjection{Error: &index.TextParameter{}}
			}
			entry.Batch = max(old.Batch, 1)
			if old.Batch == 0 {
				entry.Batch = old.ID
			}
			entry.ToolUseIDs = []string{result.ToolUseID}
			return session.Carried{Entry: entry}, nil
		}
	}
	return r.storeCarriedBytes(ctx, entry, carried.Messages)
}

func (r *Runtime) storeCarriedBytes(ctx context.Context, entry index.MessageEntry, messages []*model.Message) (session.Carried, error) {
	var content any = messages
	if len(messages) == 1 {
		entry.Kind = index.Message
		entry.Role = messages[0].Role
		content = messages[0]
	}
	data, err := json.Marshal(content, json.Deterministic(true))
	if err != nil {
		return session.Carried{}, err
	}
	value := jsontext.Value(data)
	if err := value.Canonicalize(jsontext.CanonicalizeRawInts(false), jsontext.CanonicalizeRawFloats(false)); err != nil {
		return session.Carried{}, err
	}
	hash, err := r.RunStore.Put(ctx, entry.Origin.Scope, value)
	if err != nil {
		return session.Carried{}, err
	}
	entry.Hash = hash
	size, err := contextmeasure.MeasureMessages(messages)
	if err != nil {
		return session.Carried{}, err
	}
	entry.Bytes, entry.NonText = size.Bytes, size.NonText
	return session.Carried{Entry: entry, Bytes: &hash}, nil
}

func (r *Runtime) storeCarriedUsers(ctx context.Context, in *api.SealRecordInput, entries []index.MessageEntry, lines []checkpoint.UserLine) ([]session.Carried, error) {
	out := make([]session.Carried, 0, len(lines))
	for _, line := range lines {
		entry := index.MessageEntry{ID: line.ID, Kind: index.Message, Role: model.ConversationRoleUser, Origin: line.Origin, Bytes: line.Bytes}
		if line.Origin == nil && line.ID > 0 && line.ID <= index.MessageID(len(entries)) {
			entry = entries[line.ID-1]
		}
		carried, err := r.storeCarried(ctx, in, continuation.Entry{Entry: entry, Form: continuation.Preview, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: line.Text}}}}})
		if err != nil {
			return nil, err
		}
		out = append(out, carried)
	}
	return out, nil
}

func (r *Runtime) continuationPublishRetry(ctx context.Context, in *api.SealRecordInput) (runstore.Hash, bool, error) {
	inputBytes, err := json.Marshal(in, json.Deterministic(true))
	if err != nil {
		return "", false, err
	}
	digest := sha256.Sum256(inputBytes)
	inputHash := runstore.Hash("sha256:" + hex.EncodeToString(digest[:]))
	for _, kind := range []hooks.EventType{hooks.ContinuationPublished, hooks.ContinuationSuperseded} {
		event, err := r.continuationEvent(ctx, in.RunID, kind)
		if err != nil {
			return "", false, err
		}
		if event != nil {
			if event.InputHash != inputHash {
				return "", false, engine.NewNonRetryableApplicationError("continuation body conflict", "continuation_conflict", nil, nil)
			}
			return inputHash, true, nil
		}
	}
	return inputHash, false, nil
}

func (r *Runtime) buildContinuation(ctx context.Context, in *api.SealRecordInput, head runstore.Hash) (session.Continuation, error) {
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return r.RunStore.Get(ctx, in.RootRunID, h)
	}
	entries, err := index.Walk(ctx, source, head)
	if err != nil {
		return session.Continuation{}, err
	}
	plan, err := continuationPlan(ctx, source, entries, in)
	if err != nil {
		return session.Continuation{}, err
	}

	next := session.Continuation{ID: in.RunID, Prev: in.Expected, Pos: in.Pos, Checkpoint: continuationSealView(in).Checkpoint}
	if plan.TailFrom > max(continuationSealView(in).TailFrom, 1) {
		view := continuationSealView(in)
		view.TailFrom = plan.TailFrom
		hash, err := receiptRecord(ctx, r.RunStore, in.RootRunID, source, entries, view)
		if err != nil {
			return session.Continuation{}, err
		}
		next.Checkpoint = &hash
	}
	for _, entry := range plan.Tail {
		carried, err := r.storeCarried(ctx, in, entry)
		if err != nil {
			return session.Continuation{}, err
		}
		next.Tail = append(next.Tail, carried)
	}
	if err := r.carryContinuationUsers(ctx, in, source, entries, &next); err != nil {
		return session.Continuation{}, err
	}

	next.Visible, err = r.continuationVisibleAtSeal(ctx, in, source)
	if err != nil {
		return session.Continuation{}, err
	}

	return next, nil
}

func (r *Runtime) continuationVisibleAtSeal(ctx context.Context, in *api.SealRecordInput, source index.Source) ([]runstore.Hash, error) {
	var visible []runstore.Hash
	start, err := r.loadContinuationSeed(ctx, in.RootRunID, in.RunID)
	if err != nil {
		return nil, err
	}
	if start != nil {
		visible = append(visible, start.Continuation.Visible...)
	}
	if in.FinalHash != "" {
		data, err := source(ctx, in.FinalHash)
		if err != nil {
			return nil, err
		}
		var final model.Message
		if err := json.Unmarshal(data, &final); err != nil {
			return nil, err
		}
		hash, err := continuation.VisibleHash(&final)
		if err != nil {
			return nil, err
		}
		visible = append(visible, hash)
	}
	return visible, nil
}

func (r *Runtime) carryContinuationUsers(ctx context.Context, in *api.SealRecordInput, source index.Source, entries []index.MessageEntry, next *session.Continuation) error {
	if next.Checkpoint != nil {
		view, err := checkpoint.FromRecord(ctx, source, *next.Checkpoint, entries, in.Pos)
		if err != nil {
			return err
		}
		view.Pinned = continuationSealView(in).Pinned
		pins, lines := checkpoint.CarryUsers(view, continuationSealView(in).Budget)
		next.Pins, err = r.storeCarriedUsers(ctx, in, entries, pins)
		if err != nil {
			return err
		}
		next.UserIndex, err = r.storeCarriedUsers(ctx, in, entries, lines)
		if err != nil {
			return err
		}
	}
	return nil
}
