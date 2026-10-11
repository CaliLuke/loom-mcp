package checkpoint

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCollapseWithoutHandle exercises every byte allowance near the supported
// minimum, including zero nominal component budgets and the largest labels.
func TestCollapseWithoutHandle(t *testing.T) {
	largest := int(^uint(0) >> 1)
	in := Input{ID: "record", Through: ^index.MessageID(0), CurrentPos: largest, ContinuedFrom: largest,
		Users:   []UserMessage{user(1, strings.Repeat("世界 ", 100)), user(^index.MessageID(0), "last")},
		Summary: &Summary{ID: "summary", Text: strings.Repeat("fact\n", 1000)},
		Receipt: receiptSeq([]ReceiptItem{{ToolUseID: "call", Tool: "search", Args: strings.Repeat("x", 1000)}}),
		Earlier: EarlierCounts{Calls: largest, Assistants: largest, FirstPos: 1, LastPos: largest},
	}
	for _, users := range []int{0, 1, 13, 60, 145, 300} {
		for _, donors := range []string{"framing", "receipt", "summary"} {
			b := index.RenderBudget{KeepUserTokens: users}
			minimum := MinCheckpointBytes(b)
			for extra := 0; extra <= 160; extra += 2 {
				tokens := (minimum+1)/2 - users + extra/2
				switch donors {
				case "framing":
					b.FramingTokens = tokens
				case "receipt":
					b.ReceiptTokens = tokens
				case "summary":
					b.MaxSummaryTokens = tokens
				}
				msg, err := Render(in, b, DefaultLimits, nil)
				require.NoError(t, err, "users %d donor %s extra %d", users, donors, extra)
				text := assertView(t, msg)
				assert.LessOrEqual(t, len(text), MaxBytes(b))
				assert.True(t, utf8.ValidString(text))
				assert.Contains(t, text, Handle("receipt", in.ID))
				assert.Contains(t, text, Handle("earlier", in.ID))
				assertUserDiscovery(t, msg, in)
				if users < 13 {
					assert.Contains(t, text, compactHandle("users", in.ID))
				}
				assert.Contains(t, text, fmt.Sprintf("checkpoint r%d:%s]", largest, CheckpointID("summary")))
				assert.NotContains(t, text, "recall_context")
			}
		}
	}
}

// TestRecallRenderKindSequences is the render-side Discoverable property over
// every sequence of user, assistant, call and result kinds of length ≤6.
// Results have no receipt line: their access path is the paired call's ID.
func TestRecallRenderKindSequences(t *testing.T) {
	for n := 0; n <= 6; n++ {
		for bits := 0; bits < 1<<(2*n); bits++ {
			in := Input{ID: "record", CurrentPos: 2, Through: 6, Earlier: EarlierCounts{Calls: 1, Assistants: 1, FirstPos: 1, LastPos: 1}}
			var items []ReceiptItem
			var calls []string
			paired := 0
			valid := true
			for i := 0; i < n; i++ {
				id := index.MessageID(i + 1)
				switch (bits >> (2 * i)) & 3 {
				case 0:
					in.Users = append(in.Users, user(id, "user"))
				case 1:
					items = append(items, ReceiptItem{AssistantID: id, Text: "assistant"})
				case 2:
					call := fmt.Sprintf("call-%d", id)
					calls = append(calls, call)
					items = append(items, ReceiptItem{ToolUseID: call, Tool: "search"})
				case 3:
					if paired >= len(calls) {
						valid = false
					}
					paired++
				}
			}
			if !valid {
				continue
			}
			// Reverse receipt order; swapped parallel result order has the same pure
			// receipt because it is keyed by calls, never by result entry position.
			for l, r := 0, len(items)-1; l < r; l, r = l+1, r-1 {
				items[l], items[r] = items[r], items[l]
			}
			in.Receipt = receiptSeq(items)
			for _, receipt := range []int{0, 30, 65} {
				b := index.RenderBudget{KeepUserTokens: 20, ReceiptTokens: receipt, FramingTokens: 300}
				msg, err := Render(in, b, DefaultLimits, nil)
				require.NoError(t, err)
				text := assertView(t, msg)
				assert.LessOrEqual(t, len(text), MaxBytes(b))
				assertUserDiscovery(t, msg, in)
				for _, item := range items {
					if item.ToolUseID != "" && !strings.Contains(text, item.ToolUseID+" ") {
						assert.Contains(t, text, Handle("receipt", in.ID))
					}
					if item.Text != "" && !strings.Contains(text, Label(item.AssistantID, nil, in.CurrentPos)+", assistant:") {
						assert.Contains(t, text, Handle("receipt", in.ID))
					}
				}
				assert.Contains(t, text, Handle("earlier", in.ID))
			}
		}
	}
}

func TestRecallDefaultMinimum(t *testing.T) {
	b := index.RenderBudget{KeepUserTokens: 145, MaxSummaryTokens: 290, ReceiptTokens: 87, FramingTokens: 200}
	t.Logf("B=2900: MinCheckpointBytes=%d MaxBytes=%d", MinCheckpointBytes(b), MaxBytes(b))
	assert.LessOrEqual(t, MinCheckpointBytes(b), MaxBytes(b))
}

func TestLookupReceipt(t *testing.T) {
	for _, pos := range []int{1, 2} {
		item := ReceiptItem{ToolUseID: "toolu_lookup", Tool: "runtime.recall_context", Args: strings.Repeat("界", 1000), Lookup: true, Origin: &index.Origin{Pos: 1, ID: 3}}
		got := renderReceipt(receiptSeq([]ReceiptItem{item}), EarlierCounts{}, 2000, "record", pos)
		assert.Contains(t, got, "toolu_lookup (context lookup: ")
		assert.NotContains(t, got, "recall_context")
		assert.LessOrEqual(t, len(got), MaxReceiptLineBytes)
		assert.True(t, strings.HasSuffix(got, ")\n"))
		assert.True(t, utf8.ValidString(got))
		if pos == 2 {
			assert.Contains(t, got, "r1:toolu_lookup")
		}
	}
}
