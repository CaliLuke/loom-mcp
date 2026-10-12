package runrecord

import (
	jsonopts "encoding/json"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// readOverflow verifies recovery against durable call facts and stamped settings,
// independently of current provider limits or runtime options.
func (r *runReader) readOverflow(event *runlog.Event) error {
	var overflow hooks.ContextOverflowEvent
	if err := json.Unmarshal(event.Payload, &overflow, jsonopts.FormatDurationAsNano(true)); err != nil {
		return err
	}
	if !slices.Contains(overflow.CallKeys, overflow.Call) || !slices.Equal(overflow.Calls, overflow.CallKeys) {
		return fmt.Errorf("runrecord: overflow does not adopt its failed call")
	}
	if err := r.readStep(event); err != nil {
		return err
	}
	record, ok := r.byKey[overflow.Call]
	if !ok {
		return fmt.Errorf("runrecord: overflow call is missing")
	}
	completion, err := callrecord.DecodeCompletion(record.Completion)
	if err != nil {
		return err
	}
	if completion.Facts == nil || *completion.Facts != overflow.Failure.Failed || overflow.Source != overflow.Failure.Source {
		return fmt.Errorf("runrecord: overflow facts differ from recorded call")
	}
	for _, key := range overflow.CallKeys {
		r.overflowCalls[key] = true
	}
	if overflow.Recomputed != nil {
		if r.run.ContextSettings == nil {
			return fmt.Errorf("runrecord: recovery has no stamped context settings")
		}
		entries, err := index.Walk(r.owner.ctx, r.owner.source(r.owner.record.RootRunID), overflow.IndexHead)
		if err != nil {
			return err
		}
		expected := compaction.Recover(r.state, entries, overflow.ViewRef, overflow.Failure, *r.run.ContextSettings, r.run.ContextFingerprint != "")
		if !reflect.DeepEqual(expected, *overflow.Recomputed) {
			return fmt.Errorf("runrecord: recovery differs from recorded facts")
		}
	}
	return nil
}
