package recall

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

func findHandle(nodes []recordNode, handle string) (int, string, error) {
	for i, node := range nodes {
		for _, part := range []string{"receipt", "earlier", "users"} {
			if checkpoint.Handle(part, checkpoint.CheckpointID(string(node.hash))) == handle {
				return i, part, nil
			}
		}
	}
	return 0, "", &Error{Code: CodeNotFound}
}

// runLabel recognizes only displayed rN qualifiers, never a tool use ID prefix.
func runLabel(label string) bool {
	if len(label) < 2 || label[0] != 'r' {
		return false
	}
	for _, digit := range label[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// records walks Prev before From, authorizing each run before loading its bytes.
func (r *callReader) records(ctx context.Context) ([]recordNode, error) {
	if r.req.View.Checkpoint == nil {
		return nil, nil
	}
	runID, scope, hash := r.req.RunID, r.req.Scope, *r.req.View.Checkpoint
	seen := map[string]bool{}
	var nodes []recordNode
	for hash != "" {
		if err := r.enter(ctx, runID, scope); err != nil {
			return nil, err
		}
		var nextRun *index.RunCheckpoint
		for hash != "" {
			key := runID + "\x00" + scope + "\x00" + string(hash)
			if seen[key] {
				return nil, fmt.Errorf("recall: cyclic checkpoint chain")
			}
			seen[key] = true
			data, err := r.handler.Store.Get(ctx, scope, hash)
			if err != nil {
				return nil, err
			}
			var record index.CheckpointRecord
			if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
				return nil, err
			}
			nodes = append(nodes, recordNode{run: runID, scope: scope, hash: hash, record: record})
			if nextRun == nil && record.From != nil {
				nextRun = record.From
			}
			hash = ""
			if record.Prev != nil {
				hash = *record.Prev
			}
		}
		if nextRun != nil {
			runID, scope, hash = nextRun.Run, nextRun.Scope, nextRun.Hash
		}
	}
	return nodes, nil
}

func (r *callReader) loadEntries(ctx context.Context, runID, scope string) ([]index.MessageEntry, error) {
	key := runID + "\x00" + scope
	if entries, ok := r.entries[key]; ok {
		return entries, nil
	}
	if err := r.enter(ctx, runID, scope); err != nil {
		return nil, err
	}
	head := r.req.Head
	if head == "" {
		head = r.req.View.IndexHead
	}
	if runID != r.req.RunID || scope != r.req.Scope {
		if r.handler.Chain == nil {
			return nil, &Error{Code: CodeNotFound}
		}
		var err error
		head, err = r.handler.Chain.Head(ctx, runID, scope)
		if err != nil {
			return nil, err
		}
	}
	entries, err := index.Walk(ctx, func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return r.handler.Store.Get(ctx, scope, hash)
	}, head)
	if err != nil {
		return nil, err
	}
	if runID == r.req.RunID && scope == r.req.Scope {
		entries = append(entries, r.req.View.Pending...)
	}
	r.entries[key] = entries
	return entries, nil
}

func (r *callReader) target(ctx context.Context, id, label string) (string, string, string, error) {
	if before, _, found := strings.Cut(id, ":"); found && runLabel(before) {
		if before, after, found := strings.Cut(id, ":"); found {
			if label != "" && label != before {
				return "", "", "", invalidInput()
			}
			label, id = before, after
		}
	}
	if label == "" || label == r.req.RunID || (r.req.View.ChainPos > 0 && label == fmt.Sprintf("r%d", r.req.View.ChainPos)) {
		return r.req.RunID, r.req.Scope, id, nil
	}
	if r.handler.Chain == nil {
		return "", "", "", &Error{Code: CodeNotFound}
	}
	target, err := r.handler.Chain.Resolve(ctx, r.req.RunID, label)
	if err != nil {
		return "", "", "", err
	}
	if target.Hash == "" {
		// Chain membership authorizes an earlier history even when it never compacted.
		return target.Run, target.Scope, id, nil
	}
	nodes, err := r.records(ctx)
	if err != nil {
		return "", "", "", err
	}
	for _, node := range nodes {
		if node.run == target.Run && node.scope == target.Scope && node.hash == target.Hash {
			return node.run, node.scope, id, nil
		}
	}
	return "", "", "", &Error{Code: CodeNotFound}
}
