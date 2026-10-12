package summaryeval

import (
	"encoding/json/v2"
	"slices"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Cuts returns threshold cuts in parent-first run order and canonical event order.
// Its input is a decoded RunRecord; it performs no I/O and does not mutate it.
func Cuts(rec *runrecord.RunRecord) []Cut {
	var cuts []Cut
	for i := range rec.Runs {
		r := &rec.Runs[i]
		skipped := map[int]hooks.ContextCompactionEvent{}
		for n, event := range r.Events {
			if event.Type != hooks.ContextCompacted && event.Type != hooks.ContextCompactionSkipped {
				continue
			}
			var summary hooks.ContextCompactionEvent
			if json.Unmarshal(event.Payload, &summary) != nil {
				continue
			}
			if event.Type == hooks.ContextCompactionSkipped {
				if summary.Reason == "summaries_disabled" {
					continue
				}
				skipped[summary.Step] = summary
				continue
			}
			if summary.Reason != "threshold" || (summary.Tier != originSummary && summary.Tier != "evict") || summary.Checkpoint == nil {
				continue
			}
			c, ok := makeCut(rec, r, event, n, summary, skipped)
			if !ok {
				continue
			}
			cuts = append(cuts, c)
		}
	}
	return cuts
}

func findCheckpoint(r *runrecord.Run, h runstore.Hash) (index.CheckpointRecord, bool) {
	for _, cp := range r.Checkpoints {
		if cp.Hash == h {
			return cp.Record, true
		}
	}
	return index.CheckpointRecord{}, false
}

func summaryCalls(c Cut) []runrecord.Call {
	var calls []runrecord.Call
	for _, call := range c.run.Calls {
		if call.Class == runrecord.Adopted && call.Activity == callrecord.ActivitySummarize && call.Step == c.Step && slices.Contains(c.summary.CallKeys, call.Record.Key) {
			calls = append(calls, call)
		}
	}
	slices.SortFunc(calls, func(a, b runrecord.Call) int {
		return a.Record.Key.Seq - b.Record.Key.Seq
	})
	return calls
}

func makeCut(rec *runrecord.RunRecord, r *runrecord.Run, event *runlog.Event, n int, summary hooks.ContextCompactionEvent, skipped map[int]hooks.ContextCompactionEvent) (Cut, bool) {
	cp, ok := findCheckpoint(r, *summary.Checkpoint)
	if !ok {
		return Cut{}, false
	}
	c := Cut{RunID: r.RunID, Step: summary.Step, From: 1, To: cp.Through + 1, Record: *summary.Checkpoint, Prev: cp.Prev, ChainPos: r.Pos, Origin: originSummary, record: rec, run: r, event: event, summary: summary, eventIndex: n}
	if cp.Prev != nil {
		prev, ok := findCheckpoint(r, *cp.Prev)
		if !ok {
			return Cut{}, false
		}
		c.From = prev.Through + 1
	}
	if summary.Tier == "evict" {
		prior, attempted := skipped[summary.Step]
		if attempted && prior.Reason != originRejected {
			return Cut{}, false
		}
		c.Origin = originReceipt
		if attempted {
			c.Origin = originRejected
			c.summary = prior
		}
	}
	c.Old = cutOld(c, cp)
	for _, entry := range r.Entries {
		if entry.ID >= c.From && entry.ID < c.To {
			c.EvictedBytes += entry.Bytes
		}
	}
	return c, true
}

func cutOld(c Cut, cp index.CheckpointRecord) Side {
	old := Side{Kind: c.summary.Kind, Usage: c.summary.Usage, Chunks: c.summary.Chunks, Prompt: c.summary.Prompt, Latency: time.Duration(c.summary.Latency), Fingerprint: c.run.ContextFingerprint}
	if c.Origin == originSummary && cp.Summary != nil {
		old.Text = string(c.record.Content[*cp.Summary])
		old.Checkpoint = c.Record
	}
	if c.Origin == originRejected {
		for _, call := range summaryCalls(c) {
			if call.Completion != nil && call.Completion.Response != nil {
				response, err := callrecord.DecodeResponse(c.record.Content[*call.Completion.Response])
				if err == nil {
					old.Text = responseText(response)
				}
			}
		}
	}
	old.Bytes = len(old.Text)
	return old
}
