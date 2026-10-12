package continuation

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

type (
	// Chain is anchored to the current run's recorded predecessor, never the live
	// session head. Scope IDs of published roots equal their run IDs.
	Chain struct {
		RunID, SessionID string
		Prev             *string
		Pos              int
		Sessions         session.Store
		Continuations    session.ContinuationStore
		Content          runstore.Store
		HeadOf           func(context.Context, string, string) (runstore.Hash, error)
		Authorize        func(context.Context, recall.Access) error
		predecessors     map[string]session.Continuation
	}
	chainCitations struct {
		chain   *Chain
		ctx     context.Context
		source  index.Source
		entries []index.MessageEntry
		through index.MessageID
		lines   []index.IndexLine
	}
)

// Resolve implements recall.RunChain using only committed predecessors.
func (c *Chain) Resolve(ctx context.Context, current, label string) (index.RunCheckpoint, error) {
	if current != c.RunID || c.Prev == nil {
		return index.RunCheckpoint{}, &recall.Error{Code: recall.CodeNotFound}
	}
	pos, parseErr := strconv.Atoi(strings.TrimPrefix(label, "r"))
	qualified := strings.HasPrefix(label, "r") && parseErr == nil
	if qualified && (pos < 1 || pos >= c.Pos) {
		return index.RunCheckpoint{}, &recall.Error{Code: recall.CodeNotFound}
	}
	previous := c.Prev
	seen := map[string]bool{}
	expected := c.Pos - 1
	for previous != nil {
		if seen[*previous] {
			return index.RunCheckpoint{}, fmt.Errorf("cyclic continuation chain")
		}
		seen[*previous] = true
		body, err := c.predecessor(ctx, *previous, expected)
		if err != nil {
			return index.RunCheckpoint{}, err
		}

		if (qualified && body.Pos == pos) || body.ID == label {
			target := index.RunCheckpoint{Scope: body.ID, Run: body.ID}
			if body.Checkpoint != nil {
				target.Hash = *body.Checkpoint
			}
			return target, nil
		}
		previous = body.Prev
		expected--
	}
	return index.RunCheckpoint{}, &recall.Error{Code: recall.CodeNotFound}
}

// Head implements recall.RunChain after the caller verifies session access.
func (c *Chain) Head(ctx context.Context, run, scope string) (runstore.Hash, error) {
	return c.HeadOf(ctx, run, scope)
}

// Citations adapts Chain's label resolution to checkpoint.CitationResolver.
// Carried content is preferred over an authorized read of an earlier index.
func (c *Chain) Citations(ctx context.Context, source index.Source, entries []index.MessageEntry, through index.MessageID, lines []index.IndexLine) checkpoint.CitationResolver {
	return chainCitations{chain: c, ctx: ctx, source: source, entries: entries, through: through, lines: lines}
}

func (r chainCitations) Resolve(key checkpoint.OrderKey) (checkpoint.CitationTarget, bool, error) {
	if key.Pos == 0 || key.Pos == r.chain.Pos {
		if key.ID > 0 && key.ID <= index.MessageID(len(r.entries)) && r.entries[key.ID-1].Origin != nil {
			return checkpoint.CitationTarget{}, false, nil
		}
		key.Pos = 0
		return recall.NewCitationResolver(r.ctx, r.source, r.entries, r.through).Resolve(key)
	}
	for _, entry := range r.entries {
		if entry.Origin != nil && checkpoint.Key(entry.ID, entry.Origin, r.chain.Pos) == key {
			return recall.NewCitationResolver(r.ctx, r.source, r.entries, r.through).Resolve(checkpoint.OrderKey{ID: entry.ID})
		}
	}
	for _, line := range r.lines {
		if line.Origin != nil && checkpoint.Key(line.ID, line.Origin, r.chain.Pos) == key {
			return checkpoint.CitationTarget{Kind: index.Message, Role: "user", Line: checkpoint.UserLine{ID: line.ID, Origin: line.Origin, Text: line.Text, Bytes: len(line.Text)}}, true, nil
		}
	}
	return r.resolveEarlier(key)
}

func (r chainCitations) Behind(key checkpoint.OrderKey) bool {
	if key.Pos == 0 || key.Pos == r.chain.Pos {
		return key.ID > 0 && key.ID <= r.through
	}
	for _, entry := range r.entries {
		if entry.Origin != nil && checkpoint.Key(entry.ID, entry.Origin, r.chain.Pos) == key {
			return entry.ID <= r.through
		}
	}
	return key.Pos > 0 && key.Pos < r.chain.Pos
}

func (r chainCitations) resolveEarlier(key checkpoint.OrderKey) (checkpoint.CitationTarget, bool, error) {
	target, err := r.chain.Resolve(r.ctx, r.chain.RunID, fmt.Sprintf("r%d", key.Pos))
	if err != nil {
		return checkpoint.CitationTarget{}, false, err
	}
	if r.chain.Authorize != nil {
		if err := r.chain.Authorize(r.ctx, recall.Access{SessionID: r.chain.SessionID, RunID: r.chain.RunID, TargetRunID: target.Run, Scope: target.Scope}); err != nil {
			return checkpoint.CitationTarget{}, false, err
		}
	}
	head, err := r.chain.Head(r.ctx, target.Run, target.Scope)
	if err != nil {
		return checkpoint.CitationTarget{}, false, err
	}
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return r.chain.Content.Get(ctx, target.Scope, hash)
	}
	entries, err := index.Walk(r.ctx, source, head)
	if err != nil {
		return checkpoint.CitationTarget{}, false, err
	}
	if key.ID == 0 || key.ID > index.MessageID(len(entries)) || checkpoint.Key(key.ID, entries[key.ID-1].Origin, key.Pos) != key {
		return checkpoint.CitationTarget{}, false, nil
	}
	value, found, err := recall.NewCitationResolver(r.ctx, source, entries, index.MessageID(len(entries))).Resolve(checkpoint.OrderKey{ID: key.ID})
	if found && value.Line.Origin == nil {
		value.Line.Origin = &index.Origin{Scope: target.Scope, Run: target.Run, Pos: key.Pos, ID: key.ID, Form: "full"}
	}
	return value, found, err
}

// predecessor caches the authorized immutable chain within one activity.
func (c *Chain) predecessor(ctx context.Context, id string, expected int) (session.Continuation, error) {
	if body, ok := c.predecessors[id]; ok {
		return body, nil
	}
	body, err := c.Continuations.LoadContinuation(ctx, c.SessionID, id)
	if err != nil {
		return body, &recall.Error{Code: recall.CodeUnavailable}
	}
	meta, err := c.Sessions.LoadRun(ctx, body.ID)
	if err != nil {
		return body, err
	}
	if meta.SessionID != c.SessionID || body.Pos != expected || body.ID != id {
		return body, &recall.Error{Code: recall.CodeNotFound}
	}
	if c.predecessors == nil {
		c.predecessors = make(map[string]session.Continuation)
	}
	c.predecessors[id] = body
	return body, nil
}
