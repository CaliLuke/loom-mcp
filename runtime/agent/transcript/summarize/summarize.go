// Package summarize provides pure eligible-evidence rendering, deterministic
// summary chunk planning and ordered validation. It does not load or store content.
package summarize

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Input supplies authorized canonical contents and entries. Entries include
	// bookkeeping declarations after the cut; only [From, To) is summarized.
	Input struct {
		Entries                         []index.MessageEntry
		Contents                        map[runstore.Hash][]byte
		From, To                        index.MessageID
		CurrentPos, SummaryPos          int
		Previous                        string
		AcceptsImages, AcceptsDocuments bool
		RecallNames                     []string
	}
	// Evidence keeps full eligible input, independent of planner caps or view omissions.
	Evidence struct {
		Previous    string
		Units       []Unit
		Annotations []PartAnnotation
	}
	// Unit is one labelled eligible part. Result Text is exact stored JSON, so
	// byte ranges address the original result without a decoding/re-encoding step.
	Unit struct {
		MessageID              index.MessageID
		Part                   int
		ToolUseID, Label, Text string
		Attachment             model.Part
		IsResult               bool
	}
	// PartAnnotation identifies a non-text part excluded from summary requests.
	PartAnnotation struct {
		MessageID               index.MessageID
		Part                    int
		ToolUseID, Kind, Format string
		Bytes                   int
	}
	// ByteRange is a half-open byte interval in a Unit's full eligible Text.
	// ToolUseID binds result ranges to the call's receipt; Part identifies other text.
	ByteRange struct {
		MessageID  index.MessageID
		Part       int
		ToolUseID  string
		Start, End int
		// IsResult binds ranges to original result bytes rather than call arguments.
		IsResult bool
	}
	// Piece selects one rune-aligned interval or an indivisible attachment.
	Piece struct {
		Unit       Unit
		Start, End int
	}
	// Chunk holds the new evidence and output cap for one sequential folding call.
	Chunk struct {
		Pieces       []Piece
		OutputTokens int
	}
	// ChunkOptions uses recorded summariser limits and the agent's summary cap.
	ChunkOptions struct {
		// MaxSummaryChunks and MaxSummarisedResultTokens override the default rules.
		MaxSummaryChunks          int
		MaxSummarisedResultTokens int
		Limits                    model.ContextLimits
		MaxSummaryTokens          int
		Prompt                    string
		// PromptFor measures a resolved custom template using the same quoted data as the actual call.
		PromptFor func(previous string, chunk Chunk) (string, error)
	}
	// Plan carries call inputs and annotations for integration into K12 receipts.
	Plan struct {
		Budget, OutputTokens int
		Chunks               []Chunk
		Unsummarised         []ByteRange
		Annotations          []PartAnnotation
	}
	// ValidationInput supplies the current exact summary-section byte allowance,
	// the old checkpoint plus evicted VIEW estimate, and the new rendered checkpoint
	// estimate (including reselected pins). Estimates use the same K12 units.
	ValidationInput struct {
		Text, StopReason             string
		Unfinished, DefaultPrompt    bool
		SummaryBytes, ReplacedTokens int
		RenderedCheckpoint           func(string) (int, error)
		CurrentPos                   int
		To                           index.MessageID
		Resolver                     checkpoint.CitationResolver
		RecallNames                  []string
	}
	// Validation returns canonical record-authored cited lines in citation order.
	Validation struct {
		CitedUsers []checkpoint.UserLine
		LooseRefs  int
	}
	// Rejection names the first contract failure; infrastructure errors stay errors.
	Rejection struct{ Kind string }
)

// MaxSummaryChunks bounds sequential summariser calls per compaction.
const MaxSummaryChunks = 8

// Error implements error.
func (r *Rejection) Error() string {
	return "summarize: " + r.Kind
}

// String is the receipt annotation required for an unsupported part.
func (p PartAnnotation) String() string {
	return "not summarised: " + p.Kind
}
