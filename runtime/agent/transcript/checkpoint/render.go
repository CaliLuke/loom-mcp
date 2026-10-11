package checkpoint

import (
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Input is independent of stored record encodings; K12 supplies FromRecord.
// Users contain only user-authored entries covered by Through. Receipt is a
// repeatable newest-first sequence over the whole chain, excluding Earlier.
type Input struct {
	Through       index.MessageID
	CurrentPos    int
	RunPos        map[string]int
	ID            string
	Summary       *Summary
	CitedUsers    []UserLine
	Users         []UserMessage
	Pinned        []index.MessageID
	Receipt       iter.Seq[ReceiptItem]
	Earlier       EarlierCounts
	ContinuedFrom int
}

const (
	minCheckpointBytes = 512
	userLabel          = "User messages, verbatim, oldest first. Later user text overrides earlier text, including first lines:\n"
	summaryLabel       = "\nModel-generated summary (may be wrong). User messages and the conversation below take precedence:\n"
	receiptLabel       = "\nBehind this checkpoint (framework record):\n"
)

// ErrCheckpointBudget reports a budget too small for checkpoint framing.
var ErrCheckpointBudget = errors.New("checkpoint budget is too small")

// MaxBytes converts current checkpoint token budgets at exactly two bytes/token.
func MaxBytes(b index.RenderBudget) int {
	return BytesPerToken * (b.KeepUserTokens + b.MaxSummaryTokens + b.ReceiptTokens + b.FramingTokens)
}

// MinCheckpointBytes returns the total byte threshold guaranteeing framing capacity.
// It reserves the full user allowance (at least a compact users line), the
// largest header/labels, two compact receipt lines and a compact summary closing
// line. Component token budgets must be nonnegative.
func MinCheckpointBytes(b index.RenderBudget) int {
	largest := Input{Through: ^index.MessageID(0), ContinuedFrom: int(^uint(0) >> 1)}
	framing := len(checkpointHeader(largest)) + len(userLabel) + len(summaryLabel) + len(receiptLabel)
	return max(minCheckpointBytes, max(compactUsersBytes, b.KeepUserTokens*BytesPerToken)+framing+compactReceiptBytes+compactSummaryBytes())
}

// Render constructs one bounded user-role checkpoint from record-authored inputs.
// Framing deficits consume receipt then summary bytes above their handle floors.
// Tiny component budgets borrow capacity for compact handles. K17 validates
// component budgets before loading.
func Render(in Input, b index.RenderBudget, lim Limits, pt PartTokens) (*model.Message, error) {
	minimum := MinCheckpointBytes(b)
	if MaxBytes(b) < minimum {
		return nil, fmt.Errorf("%w: %d bytes, minimum %d", ErrCheckpointBudget, MaxBytes(b), minimum)
	}
	header := checkpointHeader(in)
	framing := len(header) + len(userLabel) + len(summaryLabel) + len(receiptLabel)
	summaryBytes, receiptBytes, err := sectionBudgets(b, framing, in.Summary != nil)
	if err != nil {
		return nil, err
	}
	summary, err := renderSummary(in, summaryBytes)
	if err != nil {
		return nil, err
	}
	users := renderUsers(userInput{users: in.Users, cited: in.CitedUsers, pinned: in.Pinned, budget: b, limits: lim, partTokens: pt, currentPos: in.CurrentPos, recordID: in.ID})
	receipt := renderReceipt(in.Receipt, in.Earlier, receiptBytes, in.ID, in.CurrentPos)
	var text strings.Builder
	text.WriteString(header)
	text.WriteString(userLabel)
	text.WriteString(users.text)
	text.WriteString(summaryLabel)
	text.WriteString(summary)
	text.WriteString(receiptLabel)
	text.WriteString(receipt)
	parts := make([]model.Part, 0, len(users.parts)+1)
	parts = append(parts, model.TextPart{Text: text.String()})
	parts = append(parts, users.parts...)
	return &model.Message{Role: model.ConversationRoleUser, Parts: parts, Meta: map[string]any{transcript.CheckpointMetaKey: true}}, nil
}

func checkpointHeader(in Input) string {
	header := fmt.Sprintf("[Context checkpoint. Messages 1–%d not shown. Framework-written, not from the user.]\n\n", in.Through)
	if in.ContinuedFrom > 0 {
		header = fmt.Sprintf("Continued from run r%d.\n", in.ContinuedFrom) + header
	}
	return header
}

// sectionBudgets preserves discovery floors before charging exact framing.
// Surplus framing can fund floors when nominal component budgets are smaller.
func sectionBudgets(b index.RenderBudget, framing int, hasSummary bool) (int, int, error) {
	summaryFloor := 0
	if hasSummary {
		summaryFloor = compactSummaryBytes()
	}
	summary, receipt := max(summaryFloor, b.MaxSummaryTokens*BytesPerToken), max(compactReceiptBytes, b.ReceiptTokens*BytesPerToken)
	users := max(compactUsersBytes, b.KeepUserTokens*BytesPerToken)
	deficit := max(0, users+summary+receipt+framing-MaxBytes(b))
	take := min(deficit, receipt-compactReceiptBytes)
	receipt -= take
	deficit -= take
	take = min(deficit, summary-summaryFloor)
	summary -= take
	deficit -= take
	if deficit > 0 {
		return 0, 0, fmt.Errorf("%w: framing and handles need %d additional bytes", ErrCheckpointBudget, deficit)
	}
	return summary, receipt, nil
}

func renderSummary(in Input, maxBytes int) (string, error) {
	if in.Summary == nil {
		return "", nil
	}
	pos := in.CurrentPos
	if in.Summary.Run != "" {
		var found bool
		pos, found = in.RunPos[in.Summary.Run]
		if !found {
			return "", fmt.Errorf("checkpoint summary run %q has no chain position", in.Summary.Run)
		}
	}
	text := Qualify(in.Summary.Text, pos, in.CurrentPos)
	tokens := (len(in.Summary.Text) + BytesPerToken - 1) / BytesPerToken
	closing := fmt.Sprintf("[summary shortened from %s tokens; full text: checkpoint r%d:%s]", thousands(tokens), pos, CheckpointID(in.Summary.ID))
	if len(text) > maxBytes && len(closing) > maxBytes {
		closing = compactSummaryClosing(pos, in.Summary.ID)
	}
	if len(text) > maxBytes && len(closing) > maxBytes {
		return "", fmt.Errorf("%w: summary handle needs %d bytes", ErrCheckpointBudget, len(closing))
	}
	return BoundSummary(text, maxBytes, closing), nil
}

// compactSummaryBytes covers every valid chain position on this platform.
func compactSummaryBytes() int {
	return len(compactSummaryClosing(int(^uint(0)>>1), "record"))
}

func compactSummaryClosing(pos int, recordID string) string {
	return fmt.Sprintf("[full text: checkpoint r%d:%s]", pos, CheckpointID(recordID))
}
