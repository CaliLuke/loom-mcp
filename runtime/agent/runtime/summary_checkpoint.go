package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
)

type summaryPreparation struct {
	source              index.Source
	entries             []index.MessageEntry
	head                runstore.Hash
	evidence            summarize.Evidence
	record              index.CheckpointRecord
	allowance, replaced int
}

// prepareSummary selects only eligible hashes. It shares memoized reads across
// input, old view measurement, citation resolution and tentative checkpoint rendering.
func (r *Runtime) prepareSummary(ctx context.Context, in *SummarizeContextInput) (*summaryPreparation, error) {
	source, contents := summarySource(r.RunStore, in.Scope)
	entries, head, err := r.summaryEntries(ctx, in, source)
	if err != nil {
		return nil, err
	}
	previous, prior, err := summaryPredecessor(ctx, in, source)
	if err != nil {
		return nil, err
	}
	if err := loadSummaryContents(ctx, source, entries, in.From, in.To); err != nil {
		return nil, err
	}
	evidence, err := summarize.RenderInput(summarize.Input{Entries: entries, Contents: contents, From: in.From, To: in.To, Previous: prior, CurrentPos: in.ViewRef.ChainPos, SummaryPos: previous.SummaryPos, AcceptsImages: in.ContextModel.AcceptsImages, AcceptsDocuments: in.ContextModel.AcceptsDocuments})
	if err != nil {
		return nil, err
	}
	lines, err := receiptLines(ctx, source, entries, previous.Through, in.To)
	if err != nil {
		return nil, err
	}
	record := index.CheckpointRecord{Fingerprint: index.Fingerprint(in.ContextFingerprint), Prev: in.ViewRef.Checkpoint, Through: in.To - 1, Chain: entries[in.To-2].Chain, SummaryRun: in.RunID, SummaryPos: in.ViewRef.ChainPos, Receipt: lines}
	allowance, err := checkpoint.SummaryBytes(checkpoint.Input{Through: record.Through, Summary: &checkpoint.Summary{}}, in.Budget)
	if err != nil {
		return nil, err
	}
	preparation := &summaryPreparation{source: source, entries: entries, head: head, evidence: evidence, record: record, allowance: allowance}
	preparation.replaced, err = preparation.replacedEstimate(ctx, in)
	if err != nil {
		return nil, err
	}
	return preparation, nil
}

// loadSummaryContents fetches full eligible text even where the view renders an
// omission. Output, Record, server-only refs and injected call fields are excluded.
func loadSummaryContents(ctx context.Context, source index.Source, entries []index.MessageEntry, from, to index.MessageID) error {
	hidden := hiddenCalls(entries)
	load := func(h runstore.Hash) error {
		if h == "" {
			return nil
		}
		_, err := source(ctx, h)
		return err
	}
	for _, entry := range entries {
		if entry.ID < from || entry.ID >= to || entry.Kind == index.TypedInput {
			continue
		}
		if len(entry.ToolUseIDs) == 1 && hidden[entry.ToolUseIDs[0]] && entry.Kind == index.ToolResult {
			continue
		}
		if err := load(entry.Hash); err != nil {
			return err
		}
		for _, h := range entry.Calls {
			if err := load(h); err != nil {
				return err
			}
		}
		if entry.Kind == index.ToolResult && entry.Result != nil && entry.Result.Error != nil {
			if err := load(entry.Result.Error.Hash); err != nil {
				return err
			}
		}
		if entry.Structural != nil && entry.Structural.Kind != index.HideBookkeeping {
			for _, param := range entry.Structural.Text {
				if err := load(param.Hash); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (p *summaryPreparation) replacedEstimate(ctx context.Context, in *SummarizeContextInput) (int, error) {
	var messages []*model.Message
	if in.ViewRef.Checkpoint != nil {
		previous, err := checkpoint.FromRecord(ctx, p.source, *in.ViewRef.Checkpoint, p.entries, in.ViewRef.ChainPos)
		if err != nil {
			return 0, err
		}
		previous.Pinned = slices.Clone(in.ViewRef.Pinned)
		budget := in.ViewRef.Budget
		if budget == (index.RenderBudget{}) {
			budget = in.Budget
		}
		msg, err := checkpoint.Render(previous, budget, checkpoint.BudgetLimits(budget), nil)
		if err != nil {
			return 0, err
		}
		messages = append(messages, msg)
	}
	evicted := p.entries[in.From-1 : in.To-1]
	cleared, err := checkpoint.ResultText(ctx, p.source, evicted, in.ViewRef)
	if err != nil {
		return 0, err
	}
	tail, err := index.RenderTail(ctx, evicted, p.source, hiddenCalls(p.entries), cleared)
	if err != nil {
		return 0, err
	}
	messages = append(messages, tail...)
	return summaryEstimate(messages, in.Rate)
}

// summaryEntries verifies pending lineage before storing it, without loading server-only output metadata.
func (r *Runtime) summaryEntries(ctx context.Context, in *SummarizeContextInput, source index.Source) ([]index.MessageEntry, runstore.Hash, error) {
	entries, err := index.Walk(ctx, source, in.ViewRef.IndexHead)
	if err != nil {
		return nil, "", err
	}
	head := in.ViewRef.IndexHead
	if len(in.ViewRef.Pending) > 0 {
		var chain runstore.Hash
		if len(entries) > 0 {
			chain = entries[len(entries)-1].Chain
		}
		if err := validateSummaryPending(entries, in.ViewRef.Pending, chain); err != nil {
			return nil, "", err
		}
		chain = ""
		if len(entries) > 0 {
			chain = entries[len(entries)-1].Chain
		}
		var committed []index.MessageEntry
		head, committed, err = storeIndexSegments(ctx, r.RunStore, in.Scope, head, chain, in.ViewRef.Pending)
		if err != nil {
			return nil, "", err
		}
		entries = append(entries, committed...)
	}
	if in.To > index.MessageID(len(entries))+1 {
		return nil, "", fmt.Errorf("summary cut exceeds index")
	}
	if in.From > 1 {
		if err := validTail(entries, in.From); err != nil {
			return nil, "", err
		}
	}
	if err := validTail(entries, in.To); err != nil {
		return nil, "", err
	}
	return entries, head, nil
}

func summaryPredecessor(ctx context.Context, in *SummarizeContextInput, source index.Source) (index.CheckpointRecord, string, error) {
	var previous index.CheckpointRecord
	if in.ViewRef.Checkpoint != nil {
		data, err := source(ctx, *in.ViewRef.Checkpoint)
		if err != nil {
			return previous, "", err
		}
		if err := json.Unmarshal(data, &previous, json.RejectUnknownMembers(true)); err != nil {
			return previous, "", err
		}
	}
	if previous.Through != in.From-1 {
		return previous, "", fmt.Errorf("summary checkpoint does not match From")
	}
	prior := ""
	if previous.Summary != nil {
		if previous.SummaryRun != in.RunID && (previous.SummaryPos <= 0 || previous.SummaryPos >= in.ViewRef.ChainPos) {
			return previous, "", fmt.Errorf("summary predecessor has no recorded earlier chain position")
		}
		data, err := source(ctx, *previous.Summary)
		if err != nil {
			return previous, "", err
		}
		prior = string(data)
	}
	return previous, prior, nil
}

// summarySource shares authorized reads within one activity attempt only.
func summarySource(store runstore.Store, scope string) (index.Source, map[runstore.Hash][]byte) {
	contents := map[runstore.Hash][]byte{}
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		if data, ok := contents[h]; ok {
			return data, nil
		}
		data, err := store.Get(ctx, scope, h)
		if err != nil {
			return nil, err
		}
		contents[h] = data
		return data, nil
	}
	return source, contents
}

func validateSummaryPending(entries, pending []index.MessageEntry, chain runstore.Hash) error {
	for i, entry := range pending {
		if entry.ID != index.MessageID(len(entries)+i+1) {
			return fmt.Errorf("summary pending index gap")
		}
		expected, err := index.ComputeChain(chain, entry)
		if err != nil {
			return err
		}
		if entry.Chain != expected {
			return fmt.Errorf("summary pending chain mismatch")
		}
		chain = entry.Chain
	}
	return nil
}
