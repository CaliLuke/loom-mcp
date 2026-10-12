package index

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// Fingerprint identifies the context configuration recorded with a checkpoint.
	Fingerprint string
	// RunCheckpoint qualifies a checkpoint address by its root scope and run.
	RunCheckpoint struct {
		// Scope owns the checkpoint content.
		Scope string `json:"scope"`
		// Run identifies the publishing run.
		Run string `json:"run"`
		// Hash addresses that run's checkpoint.
		Hash runstore.Hash `json:"hash"`
	}
	// IndexLine is a bounded user-message index line with citation provenance.
	IndexLine struct {
		// ID is the indexed message position.
		ID MessageID `json:"id"`
		// Text is the bounded first line.
		Text string `json:"text"`
		// Origin qualifies a carried message.
		Origin *Origin `json:"origin,omitzero"`
	}
	// ByteRange is an inclusive unsummarised range in the original result.
	ByteRange struct {
		From    int `json:"from"`
		Through int `json:"through"`
	}
	// ReceiptLine describes one evicted call, assistant message or unsummarised range.
	ReceiptLine struct {
		// Kind is call, assistant or part.
		Kind string `json:"kind"`
		// MessageID identifies the entry containing an unsummarised part.
		MessageID MessageID `json:"message_id,omitzero"`
		// Part is the one-based part within MessageID.
		Part int `json:"part,omitzero"`
		// Format and Bytes describe an unaccepted image or document.
		Format string `json:"format,omitzero"`
		Bytes  int    `json:"bytes,omitzero"`
		// ToolUseID identifies a covered model-eligible call.
		ToolUseID string `json:"tool_use_id,omitzero"`
		// Unsummarised names result byte ranges not yet summarised.
		Unsummarised []ByteRange `json:"unsummarised,omitzero"`
		// From is the first covered message.
		From MessageID `json:"from"`
		// Through is the last covered message.
		Through MessageID `json:"through"`
		// Tool names an evicted tool call, if any.
		Tool string `json:"tool,omitzero"`
		// Text contains the bounded argument preview or assistant first line.
		Text string `json:"text,omitzero"`
		// Origin qualifies a carried receipt.
		Origin *Origin `json:"origin,omitzero"`
	}
	// EarlierReceipt summarizes history preceding a continued run's checkpoint.
	EarlierReceipt struct {
		// Calls and Assistants count collapsed receipt lines across earlier runs.
		Calls      int `json:"calls,omitzero"`
		Assistants int `json:"assistants,omitzero"`

		// Runs counts earlier runs.
		Runs int `json:"runs"`
		// Messages counts earlier messages.
		Messages int `json:"messages"`
		// First is the first earlier run position.
		First int `json:"first"`
		// Last is the last earlier run position.
		Last int `json:"last"`
		// Users holds the bounded carried user index lines.
		Users []IndexLine `json:"users"`
	}
	// CheckpointRecord is immutable compaction evidence. K8 resolves records but
	// does not produce them; K12/K15 own checkpoint production and projection.
	CheckpointRecord struct {
		// Prev addresses the preceding record within this run.
		Prev *runstore.Hash `json:"prev"`
		// From qualifies the previous run's latest checkpoint for continuation.
		From *RunCheckpoint `json:"from"`
		// Earlier summarizes history behind From.
		Earlier EarlierReceipt `json:"earlier"`
		// Summary addresses the latest summary text, if any.
		Summary *runstore.Hash `json:"summary"`
		// SummaryRun is the run against which bare summary citations resolve.
		SummaryRun string `json:"summary_run"`
		// SummaryPos binds copied bare citations to their original chain position.
		SummaryPos int `json:"summary_pos,omitzero"`
		// CitedUsers are the user index lines cited by the summary.
		CitedUsers []IndexLine `json:"cited_users"`
		// Receipt contains only the receipt lines added by this record.
		Receipt []ReceiptLine `json:"receipt"`
		// Through is the last entry covered by the checkpoint.
		Through MessageID `json:"through"`
		// Chain binds the entries through Through.
		Chain runstore.Hash `json:"chain"`
		// Fingerprint identifies the context configuration.
		Fingerprint Fingerprint `json:"fingerprint"`
		// Prompt identifies the prompt that produced Summary.
		Prompt *prompt.PromptRef `json:"prompt"`
		// Calls names every summarizer invocation behind the summary.
		Calls []runstore.CallKey `json:"calls"`
	}
)
