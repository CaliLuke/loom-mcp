package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// workflowPlanInput keeps transcript history as references. Loaded planner messages
// never become workflow state; content holds only the current uncommitted step.
type workflowPlanInput struct {
	AgentID                 agent.Ident
	RunContext              run.Context
	Entries                 []index.MessageEntry
	IndexHead               runstore.Hash
	Committed               int
	CallRefs                map[string]runstore.Hash
	unindexedCalls          []planner.ToolRequest
	unindexedResults        map[string]*planner.ToolResult
	content                 *workflowContentBatch
	protocolVersion         int
	step                    int
	requestGrowthPublished  bool
	producingCall           *runstore.CallKey
	transcriptSize          contextmeasure.MessageSize
	compactionView          index.ViewRef
	checkpointBytes         int
	compaction              *ContextCompaction
	contextSettings         compaction.ContextSettings
	contextFingerprint      string
	compactionState         compaction.State
	compactionDecision      compaction.Decision
	compactionFlags         compaction.Flags
	compactionSuppressed    bool
	summaryApplied          bool
	summariesOffLogged      bool
	recovery                ContextRecovery
	recallAdvertised        bool
	recallAvailable         bool
	recallDisabledPublished bool
	recallCharges           map[string]recallCharge
	recallExhausted         map[bool]bool
	recallEpisode           int
}

func workflowText(ctx context.Context, text string) (index.TextParameter, error) {
	if len(text) <= index.MaxInlineTextBytes {
		return index.TextParameter{Text: text}, nil
	}
	refs, err := recordWorkflowContent(ctx, [][]byte{[]byte(text)})
	if err != nil {
		return index.TextParameter{}, err
	}
	return index.TextParameter{Hash: refs[0]}, nil
}

func planToolFilter(input *RunInput) api.PlanToolFilter {
	if input.Policy == nil {
		return api.PlanToolFilter{}
	}
	return api.PlanToolFilter{RestrictToTool: input.Policy.RestrictToTool, AllowedTags: input.Policy.AllowedTags, DeniedTags: input.Policy.DeniedTags}
}

func (b *workflowPlanInput) appendEntry(entry index.MessageEntry) error {
	if err := b.measureStructuralEntry(&entry, b.content); err != nil {
		return err
	}
	entry.ID = index.MessageID(len(b.Entries) + 1)
	var prev runstore.Hash
	if len(b.Entries) > 0 {
		prev = b.Entries[len(b.Entries)-1].Chain
	}
	chain, err := index.ComputeChain(prev, entry)
	if err != nil {
		return err
	}
	entry.Chain = chain
	b.Entries = append(b.Entries, entry)
	return nil
}

func (b *workflowPlanInput) view() index.ViewRef {
	view := b.compactionView
	view.IndexHead = b.IndexHead
	view.Pending = append([]index.MessageEntry(nil), b.Entries[b.Committed:]...)
	view.Pinned = append([]index.MessageID(nil), view.Pinned...)
	return view
}

func (b *workflowPlanInput) adopt(out *PlanActivityOutput) {
	b.adoptHead(out.IndexHead)
	b.producingCall = out.ProducingCall
	b.transcriptSize = out.TranscriptSize
	b.CallRefs = make(map[string]runstore.Hash)
	if out.Result != nil {
		for i, call := range out.Result.ToolCalls {
			if i < len(out.ToolCallRefs) {
				b.CallRefs[call.ToolCallID] = out.ToolCallRefs[i]
			}
		}
	}
	for id, ref := range out.AwaitCallRefs {
		b.CallRefs[id] = ref
	}
}

// adoptHead commits the current pending entries without replacing held call refs.
// Adopting an already-current head with no pending entries is a no-op.
func (b *workflowPlanInput) adoptHead(head runstore.Hash) {
	b.IndexHead = head
	b.Committed = len(b.Entries)
}

func (b *workflowPlanInput) recordAssistant(ref runstore.Hash, calls []planner.ToolRequest) error {
	if ref == "" && len(calls) == 0 {
		return nil
	}
	entry := index.MessageEntry{Kind: index.Assistant, Hash: ref, Call: b.producingCall, Role: model.ConversationRoleAssistant}
	if ref != "" {
		entry.Bytes, entry.NonText = b.transcriptSize.Bytes, b.transcriptSize.NonText
	}
	var parts []model.Part
	for _, call := range calls {
		hash := b.CallRefs[call.ToolCallID]
		if hash == "" {
			return fmt.Errorf("missing stored call %s", call.ToolCallID)
		}
		entry.Calls = append(entry.Calls, hash)
		entry.ToolUseIDs = append(entry.ToolUseIDs, call.ToolCallID)
		parts = append(parts, model.ToolUsePart{ID: call.ToolCallID, Name: string(call.Name), Input: call.Payload})
	}
	size, err := contextmeasure.MeasureMessages([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: parts}})
	if err != nil {
		return err
	}
	entry.Bytes += size.Bytes
	entry.NonText += size.NonText
	if err := b.appendEntry(entry); err != nil {
		return err
	}
	for _, call := range calls {
		b.noteIndexedCall(call)
	}
	return nil
}

func (b *workflowPlanInput) structural(kind index.StructuralKind, ids []string) error {
	return b.appendEntry(index.MessageEntry{Kind: index.Structural, ToolUseIDs: ids, Structural: &index.StructuralEntry{Kind: kind, ProtocolVersion: index.TemplateProtocolVersion}})
}
