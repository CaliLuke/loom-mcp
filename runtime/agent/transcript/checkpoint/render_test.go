package checkpoint

import (
	"embed"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/*.golden
var goldenFiles embed.FS

// TestRenderGolden checks chronological checkpoint text against reviewed fixtures.
func TestRenderGolden(t *testing.T) {
	cases := []struct {
		name string
		in   Input
	}{
		{"correction", Input{ID: "correction", Through: 212, CurrentPos: 1, Users: []UserMessage{user(1, "use production"), user(57, "use staging only"), user(98, "skip migration"), user(140, "done")}, Pinned: []index.MessageID{1, 140}, Summary: &Summary{Run: "current", ID: "summary", Text: "# Decisions\nUse staging only [message 57].\n"}, RunPos: map[string]int{"current": 1}}},
		{"cross_run", Input{ID: "cross-run", Through: 200, CurrentPos: 3, ContinuedFrom: 2, Users: []UserMessage{user(1, "use staging"), user(2, "use production"), user(5, "use development")}, Pinned: []index.MessageID{1, 2, 5}, Summary: &Summary{Run: "one", ID: "old-summary", Text: "Earlier choice [message 57]."}, RunPos: map[string]int{"one": 1}}},
	}
	cases[1].in.Users[0].Origin = &index.Origin{Pos: 1, ID: 57}
	cases[1].in.Users[1].Origin = &index.Origin{Pos: 2, ID: 5}
	budget := index.RenderBudget{KeepUserTokens: 300, MaxSummaryTokens: 100, ReceiptTokens: 200, FramingTokens: 200}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := Render(tt.in, budget, DefaultLimits, nil)
			require.NoError(t, err)
			text := assertView(t, msg)
			golden := filepath.Join("testdata", tt.name+".golden")
			want, err := goldenFiles.ReadFile(golden)
			require.NoError(t, err)
			assert.Equal(t, string(want), text)
			if tt.name == "cross_run" {
				assert.Less(t, strings.Index(text, "r1 message 57"), strings.Index(text, "r2 message 5"))
				assert.Less(t, strings.Index(text, "r2 message 5"), strings.Index(text, "[message 5]"))
				assert.Contains(t, text, "[r1 message 57]")
			}
		})
	}
}

// TestRenderMinimum checks the supported floor and complete-user framing reserve.
func TestRenderMinimum(t *testing.T) {
	for _, tt := range []struct {
		users   int
		minimum int
	}{{0, 512}, {60, 512}, {127, 646}, {3000, 6392}} {
		b := index.RenderBudget{KeepUserTokens: tt.users}
		assert.Equal(t, tt.minimum, MinCheckpointBytes(b))
		in := Input{Through: ^index.MessageID(0), ContinuedFrom: int(^uint(0) >> 1)}
		for _, delta := range []int{-2, 0, 2} {
			b.FramingTokens = (tt.minimum+delta)/BytesPerToken - b.KeepUserTokens
			msg, err := Render(in, b, DefaultLimits, nil)
			if delta < 0 {
				require.ErrorIs(t, err, ErrCheckpointBudget)
				assert.Nil(t, msg)
				continue
			}
			require.NoError(t, err)
			assert.LessOrEqual(t, len(assertView(t, msg)), MaxBytes(b))
		}
	}
}

// TestRenderBudgetCases preserves seed 1102 trial 24 and donor-budget boundaries.
func TestRenderBudgetCases(t *testing.T) {
	trial24 := index.RenderBudget{KeepUserTokens: 127, MaxSummaryTokens: 60, ReceiptTokens: 26, FramingTokens: 53, PreviewTokens: 294}
	for _, tt := range []struct {
		name    string
		budget  index.RenderBudget
		minimum int
		below   bool
	}{
		{"zero", index.RenderBudget{}, 512, true},
		{"seed 1102 trial 24", trial24, 646, true},
		{"donors two bytes short", index.RenderBudget{KeepUserTokens: 127, MaxSummaryTokens: 60, ReceiptTokens: 82, FramingTokens: 53}, 646, true},
		{"donors exact", index.RenderBudget{KeepUserTokens: 127, MaxSummaryTokens: 60, ReceiptTokens: 83, FramingTokens: 53}, 646, false},
		{"summary alone", index.RenderBudget{KeepUserTokens: 127, MaxSummaryTokens: 196}, 646, false},
		{"receipt alone", index.RenderBudget{KeepUserTokens: 127, ReceiptTokens: 196}, 646, false},
		{"framing alone", index.RenderBudget{KeepUserTokens: 127, FramingTokens: 196}, 646, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.minimum, MinCheckpointBytes(tt.budget))
			in := Input{Through: ^index.MessageID(0), ContinuedFrom: int(^uint(0) >> 1)}
			if tt.name == "seed 1102 trial 24" {
				in = Input{Through: 10000, ContinuedFrom: 2, CurrentPos: 3}
			}
			msg, err := Render(in, tt.budget, DefaultLimits, nil)
			if tt.below {
				require.ErrorIs(t, err, ErrCheckpointBudget)
				assert.Nil(t, msg)
				if err != nil {
					assert.Contains(t, err.Error(), "minimum")
				}
				return
			}
			require.NoError(t, err)
			assert.LessOrEqual(t, len(assertView(t, msg)), MaxBytes(tt.budget))
		})
	}
}

// TestRenderProperty checks seeded byte/part bounds, discovery and encoded determinism.
func TestRenderProperty(t *testing.T) {
	rng := &testGenerator{state: 1102}
	accepted, rejected := 0, 0
	for trial := 0; trial < 160; trial++ {
		in := randomInput(rng, trial)
		b := index.RenderBudget{KeepUserTokens: rng.Intn(3000), MaxSummaryTokens: rng.Intn(1000), ReceiptTokens: rng.Intn(500), FramingTokens: rng.Intn(257), PreviewTokens: rng.Intn(500)}
		if trial%4 == 0 {
			b = index.RenderBudget{KeepUserTokens: rng.Intn(129), MaxSummaryTokens: rng.Intn(65), ReceiptTokens: rng.Intn(33), FramingTokens: rng.Intn(65), PreviewTokens: rng.Intn(500)}
		}
		lim := DefaultLimits
		in.Pinned = SelectPins(in.Users, b, lim)
		measure := func(part model.Part) int {
			switch value := part.(type) {
			case model.ImagePart:
				return len(value.Bytes) + 1
			case model.DocumentPart:
				return len(value.Bytes)/2 + 1
			default:
				return 1
			}
		}
		msg, err := Render(in, b, lim, measure)
		if MaxBytes(b) < MinCheckpointBytes(b) {
			rejected++
			require.ErrorIs(t, err, ErrCheckpointBudget, "trial %d budget %+v", trial, b)
			assert.Nil(t, msg)
			continue
		}
		accepted++
		require.NoError(t, err, "trial %d budget %+v", trial, b)
		text := assertView(t, msg)
		assert.LessOrEqual(t, len(text), MaxBytes(b), "trial %d", trial)
		again, err := Render(in, b, lim, measure)
		require.NoError(t, err)
		assert.Equal(t, msg, again)
		encoded, err := json.Marshal(msg, json.Deterministic(true))
		require.NoError(t, err)
		repeated, err := json.Marshal(again, json.Deterministic(true))
		require.NoError(t, err)
		assert.Equal(t, encoded, repeated)
		assert.LessOrEqual(t, len(msg.Parts)-1, lim.MaxPinnedParts)
		total := 0
		for _, part := range msg.Parts[1:] {
			total += measure(part)
		}
		assert.LessOrEqual(t, total, b.KeepUserTokens/4)
		if b.KeepUserTokens >= 256 {
			assertUserDiscovery(t, msg, in)
		}
	}
	assert.Positive(t, accepted)
	assert.Positive(t, rejected)
}

// TestRenderShrunkBudget bounds a stored large summary and a 10,000-item receipt.
func TestRenderShrunkBudget(t *testing.T) {
	items := make([]ReceiptItem, 10000)
	for i := range items {
		items[i] = ReceiptItem{AssistantID: index.MessageID(10000 - i), Text: "assistant decision"}
	}
	in := Input{ID: "record", Through: 10000, CurrentPos: 2, Summary: &Summary{Run: "previous", ID: "summary", Text: "# Goal\n" + strings.Repeat("fact\n", 6400)}, RunPos: map[string]int{"previous": 1}, Receipt: receiptSeq(items)}
	b := index.RenderBudget{KeepUserTokens: 200, MaxSummaryTokens: 1000, ReceiptTokens: 500, FramingTokens: 200}
	msg, err := Render(in, b, DefaultLimits, nil)
	require.NoError(t, err)
	text := assertView(t, msg)
	assert.LessOrEqual(t, len(text), MaxBytes(b))
	assert.Contains(t, text, "summary shortened from 16,004 tokens")
	assert.Contains(t, text, "full text: checkpoint r1:ck-")
	assert.Contains(t, text, "[receipt rc-")
	assert.Contains(t, text, "message 10000, assistant")
}

// TestRenderNoToolName checks that framework renderings recommend no tool.
func TestRenderNoToolName(t *testing.T) {
	in := Input{ID: "record", CurrentPos: 1, Through: 3, Users: []UserMessage{user(1, "task")}, Pinned: []index.MessageID{1}, Summary: &Summary{Run: "current", ID: "summary", Text: strings.Repeat("fact\n", 1000)}, RunPos: map[string]int{"current": 1}, Receipt: receiptSeq([]ReceiptItem{{ToolUseID: "toolu", Tool: "search", Args: "id=2"}})}
	b := index.RenderBudget{KeepUserTokens: 300, MaxSummaryTokens: 100, ReceiptTokens: 100, FramingTokens: 200}
	msg, err := Render(in, b, DefaultLimits, nil)
	require.NoError(t, err)
	assert.NotContains(t, assertView(t, msg), "recall_context")
	assert.NotContains(t, ClearedResult(100, "toolu", "failed"), "recall_context")
}

// TestRenderFramingDeficit bounds rendered text when framing exceeds its allowance.
func TestRenderFramingDeficit(t *testing.T) {
	b := index.RenderBudget{KeepUserTokens: 100, MaxSummaryTokens: 500, ReceiptTokens: 500, FramingTokens: 1}
	in := Input{ID: "record", CurrentPos: 1, Summary: &Summary{Run: "current", Text: strings.Repeat("line\n", 500)}, RunPos: map[string]int{"current": 1}}
	msg, err := Render(in, b, DefaultLimits, nil)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(assertView(t, msg)), MaxBytes(b))
	// Exact framing consumes receipt first; an absent receipt cannot donate its
	// unused capacity to enlarge the summary beyond its own current budget.
	assert.LessOrEqual(t, len(summaryText(msg)), b.MaxSummaryTokens*2)
}

// TestSectionBudgets checks receipt-first then summary framing donations.
func TestSectionBudgets(t *testing.T) {
	budget := index.RenderBudget{MaxSummaryTokens: 100, ReceiptTokens: 100, FramingTokens: 100}
	for _, tt := range []struct{ framing, summary, receipt int }{{150, 200, 200}, {250, 200, 150}, {450, 150, 0}, {600, 0, 0}} {
		summary, receipt, err := sectionBudgets(budget, tt.framing)
		require.NoError(t, err)
		assert.Equal(t, tt.summary, summary)
		assert.Equal(t, tt.receipt, receipt)
	}
	_, _, err := sectionBudgets(budget, 601)
	assert.ErrorIs(t, err, ErrCheckpointBudget)
}

func assertView(t *testing.T, msg *model.Message) string {
	t.Helper()
	view := transcript.AssembleView(nil, msg, []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "intact", Signature: "signature"}, model.TextPart{Text: "tail"}}}})
	assert.NoError(t, transcript.ValidateView(view, transcript.GrammarBedrock{Thinking: true}))
	assert.NoError(t, transcript.ValidateView(view, transcript.GrammarAnthropic{}))
	require.NotEmpty(t, msg.Parts)
	text, ok := msg.Parts[0].(model.TextPart)
	require.True(t, ok)
	return text.Text
}

func randomInput(rng *testGenerator, trial int) Input {
	in := Input{ID: fmt.Sprintf("record-%d", trial), Through: 10000, CurrentPos: 3, RunPos: map[string]int{"one": 1, "two": 2, "current": 3}, ContinuedFrom: 2}
	count := rng.Intn(501)
	for i := 0; i < count; i++ {
		u := user(index.MessageID(i+1), strings.Repeat("世界 text\n", rng.Intn(100)))
		if i%3 != 2 {
			u.Origin = &index.Origin{Pos: i%3 + 1, ID: index.MessageID(i/3 + 1)}
		}
		if rng.Intn(3) == 0 {
			for p := 0; p < rng.Intn(5)+1; p++ {
				if p%2 == 0 {
					u.Message.Parts = append(u.Message.Parts, model.ImagePart{Format: "png", Bytes: make([]byte, rng.Intn(800)+1)})
				} else {
					u.Message.Parts = append(u.Message.Parts, model.DocumentPart{Name: "attachment", Format: "pdf", Bytes: make([]byte, rng.Intn(800)+1)})
				}
			}
		}
		in.Users = append(in.Users, u)
		if rng.Intn(10) == 0 {
			in.CitedUsers = append(in.CitedUsers, UserLine{ID: u.ID, Origin: u.Origin, Text: "forged"})
		}
	}
	in.Summary = &Summary{Run: []string{"one", "two", "current"}[rng.Intn(3)], ID: "summary", Text: "# Goal\n" + strings.Repeat("facts\n", rng.Intn(21844))}
	items := make([]ReceiptItem, rng.Intn(10001))
	for i := range items {
		id := index.MessageID(len(items)) - index.MessageID(i)
		items[i] = ReceiptItem{ToolUseID: fmt.Sprintf("call-%d", id), Tool: "search", Args: strings.Repeat("arguments ", rng.Intn(30)), AssistantID: id, Text: "decision"}
	}
	in.Receipt = receiptSeq(items)
	in.Earlier = EarlierCounts{Calls: rng.Intn(10000), Assistants: rng.Intn(1000), FirstPos: 1, LastPos: 2}
	return in
}

func summaryText(msg *model.Message) string {
	text := msg.Parts[0].(model.TextPart).Text
	_, rest, _ := strings.Cut(text, summaryLabel)
	summary, _, _ := strings.Cut(rest, receiptLabel)
	return summary
}

func assertUserDiscovery(t *testing.T, msg *model.Message, in Input) {
	t.Helper()
	text := msg.Parts[0].(model.TextPart).Text
	_, section, _ := strings.Cut(text, userLabel)
	section, _, _ = strings.Cut(section, summaryLabel)
	for _, user := range in.Users {
		label := Label(user.ID, user.Origin, in.CurrentPos)
		if !strings.Contains(section, "["+label+"] ") && !strings.Contains(section, "["+label+", first line] ") {
			assert.Contains(t, section, "[users rc-")
			return
		}
	}
}
