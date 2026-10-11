package checkpoint

import (
	"crypto/sha256"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
)

// TestHandles checks the specified eight-hex SHA-256 identifiers.
func TestHandles(t *testing.T) {
	sum := sha256.Sum256([]byte("usersrecord"))
	assert.Equal(t, fmt.Sprintf("rc-%x", sum[:4]), Handle("users", "record"))
	sum = sha256.Sum256([]byte("record"))
	assert.Equal(t, fmt.Sprintf("ck-%x", sum[:4]), CheckpointID("record"))
	assert.NotEqual(t, Handle("users", "record"), Handle("receipt", "record"))
}

// TestReceipt checks assistant discovery, qualified calls and unsummarised ranges.
func TestReceipt(t *testing.T) {
	items := []ReceiptItem{
		{AssistantID: 57, Text: "decision line\nsecret second line"},
		{ToolUseID: "toolu_123", Tool: "search", Args: "customer=42", Unsummarised: []ByteRange{{From: 40000, Through: 1200000}}},
	}
	got := renderReceipt(receiptSeq(items), EarlierCounts{}, 1000, "record", 1)
	assert.Contains(t, got, "message 57, assistant: \"decision line\"")
	assert.NotContains(t, got, "secret second")
	assert.Contains(t, got, "toolu_123 search(customer=42)")
	assert.Contains(t, got, "bytes 40,000–1,200,000 not summarised")
	one := renderReceipt(receiptSeq(items[:1]), EarlierCounts{}, 100, "record", 1)
	assert.Contains(t, one, "message 57, assistant: \"decision line\"")
	earlier := EarlierCounts{Calls: 112, Assistants: 40, FirstPos: 1, LastPos: 2}
	got = renderReceipt(receiptSeq(items), earlier, 350, "record", 3)
	assert.Contains(t, got, "112 calls and 40 assistant messages in earlier runs r1–r2")
	assert.Regexp(t, "\\[receipt rc-[0-9a-f]{8}\\]", got)
	qualified := renderReceipt(receiptSeq([]ReceiptItem{{AssistantID: 5, Text: "old", Origin: &index.Origin{Pos: 1, ID: 5}}, {ToolUseID: "oldcall", Tool: "search", Origin: &index.Origin{Pos: 1, ID: 4}}}), EarlierCounts{}, 1000, "record", 2)
	assert.Contains(t, qualified, "r1 message 5, assistant")
	assert.Contains(t, qualified, "r1:oldcall")
	misleadingArgs := []ReceiptItem{{ToolUseID: "call", Tool: "tool", Args: ", assistant: \"" + strings.Repeat("x", 1000), AssistantID: 5, Text: "decision"}}
	collapsed := renderReceipt(receiptSeq(misleadingArgs), EarlierCounts{}, 200, "record", 1)
	assert.Contains(t, collapsed, "1 earlier calls and 1 assistant messages")
}

// TestReceiptBound checks newest-line selection, fixed line caps and aggregate handles.
func TestReceiptBound(t *testing.T) {
	items := make([]ReceiptItem, 10000)
	for i := range items {
		items[i] = ReceiptItem{ToolUseID: fmt.Sprintf("toolu_%d", 10000-i), Tool: "search", Args: strings.Repeat("x", 1000)}
	}
	for _, budget := range []int{0, 200, 1000, 4000} {
		got := renderReceipt(receiptSeq(items), EarlierCounts{Calls: 100, FirstPos: 1, LastPos: 2}, budget, "record", 3)
		assert.LessOrEqual(t, len(got), budget)
		assert.Equal(t, got, renderReceipt(receiptSeq(items), EarlierCounts{Calls: 100, FirstPos: 1, LastPos: 2}, budget, "record", 3))
		if budget >= 200 {
			assert.Contains(t, got, "[receipt rc-")
			for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
				assert.LessOrEqual(t, len(line), MaxReceiptLineBytes)
				if strings.HasPrefix(line, "…") {
					assert.Contains(t, line, "[receipt rc-")
				}
			}
		}
		if budget >= 1000 {
			assert.Contains(t, got, "toolu_10000")
			assert.NotContains(t, got, "toolu_1 search")
		}
	}
}

func receiptSeq(items []ReceiptItem) iter.Seq[ReceiptItem] {
	return func(yield func(ReceiptItem) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}
