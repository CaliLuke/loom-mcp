package runrecord

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type collectingStore struct {
	runstore.Store
	reader *reader
}

func (s collectingStore) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	return s.reader.source(scope)(ctx, hash)
}

func (r *runReader) readEntryContent() error {
	for _, entry := range r.run.Entries {
		target := r.contentTarget(entry.Kind)
		if entry.Hash != "" && target != nil {
			if err := r.addPart(target, entry, entry.Hash); err != nil {
				return err
			}
		}
		for _, hash := range entry.Record {
			if err := r.addPart(&r.run.ServerOnly, entry, hash); err != nil {
				return err
			}
		}
		if entry.Output != "" {
			if err := r.readOutput(entry); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *runReader) contentTarget(kind index.EntryKind) *[]ContentPart {
	switch kind {
	case index.TypedInput:
		return &r.run.TypedAnswers
	case index.Clarification:
		return &r.run.AwaitAnswers
	case index.ToolResult:
		return &r.run.Results
	case index.Message, index.Assistant, index.Structural:
		return nil
	default:
		return nil
	}
}

func (r *runReader) addPart(target *[]ContentPart, entry index.MessageEntry, hash runstore.Hash) error {
	data, err := r.owner.source(r.owner.record.RootRunID)(r.owner.ctx, hash)
	if err != nil {
		return err
	}
	for _, part := range *target {
		if part.EntryID == entry.ID && part.Hash == hash {
			return nil
		}
	}
	*target = append(*target, ContentPart{EntryID: entry.ID, InputID: entry.InputID, Hash: hash, Content: data})
	return nil
}

func (r *runReader) readOutput(entry index.MessageEntry) error {
	source := r.owner.source(r.owner.record.RootRunID)
	data, err := source(r.owner.ctx, entry.Output)
	if err != nil {
		return err
	}
	var output planner.ToolOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return fmt.Errorf("decode output: %w", err)
	}
	for _, part := range []struct {
		hash   runstore.Hash
		target *[]ContentPart
	}{
		{output.Refs.Result, &r.run.Results}, {output.Refs.ServerData, &r.run.ServerOnly}, {output.Refs.Payload, &r.run.ServerOnly},
	} {
		if part.hash != "" {
			if err := r.addPart(part.target, entry, part.hash); err != nil {
				return err
			}
		}
	}
	if output.Call != "" {
		if _, err := source(r.owner.ctx, output.Call); err != nil {
			return err
		}
	}
	return nil
}

func (r *runReader) checkpoint(scope string, hash runstore.Hash, active map[string]bool) error {
	key := scope + "\x00" + string(hash)
	if active[key] {
		return fmt.Errorf("runrecord: cyclic checkpoint at %s", hash)
	}
	if r.checkpoints[key] {
		return nil
	}
	active[key] = true
	defer delete(active, key)
	data, err := r.owner.source(scope)(r.owner.ctx, hash)
	if err != nil {
		return err
	}
	var record index.CheckpointRecord
	if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
		return fmt.Errorf("decode checkpoint: %w", err)
	}
	if record.Prev != nil {
		if err := r.checkpoint(scope, *record.Prev, active); err != nil {
			return err
		}
	}
	if record.From != nil {
		if record.From.Scope == "" || record.From.Run == "" {
			return fmt.Errorf("runrecord: checkpoint From requires scope and run")
		}
		if err := r.checkpoint(record.From.Scope, record.From.Hash, active); err != nil {
			return err
		}
	}
	if record.Summary != nil {
		if _, err := r.owner.source(scope)(r.owner.ctx, *record.Summary); err != nil {
			return err
		}
	}
	r.run.Checkpoints = append(r.run.Checkpoints, Checkpoint{Scope: scope, Hash: hash, Record: record})
	r.checkpoints[key] = true
	return nil
}
