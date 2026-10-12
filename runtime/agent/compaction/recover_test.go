package compaction

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

func TestRecoverRecordedRequest(t *testing.T) {
	entries := []index.MessageEntry{
		{ID: 1, Kind: index.Message, Role: "user", Bytes: 100},
		{ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 50, ToolUseIDs: []string{"old"}},
		{ID: 3, Kind: index.ToolResult, Role: "user", Bytes: 30000, Hash: "old", ToolUseIDs: []string{"old"}},
		{ID: 4, Kind: index.Assistant, Role: "assistant", Bytes: 50, ToolUseIDs: []string{"new"}},
		{ID: 5, Kind: index.ToolResult, Role: "user", Bytes: 12000, Keep: true, Hash: "new", ToolUseIDs: []string{"new"}},
	}
	failure := contextmeasure.ContextOverflow{Source: "provider", Failed: contextmeasure.RequestFacts{Model: "m", ViewBearing: true, ContextWindow: 10000, TextBytes: 43200, ViewTextBytes: 42200}}
	state := Observe(State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 10000, InputTokens: 14400, TextBytes: 43200}})
	got := Recover(state, entries, index.ViewRef{}, failure, Settings{MinSavingTokens: 99999, ProtectRecentToolTokens: 99999}, true)
	assert.Equal(t, Clear, got.Tier, "recovery ignores protected and minimum-saving windows")
	assert.Equal(t, index.MessageID(3), got.View.ClearedThrough)
	assert.True(t, got.Reached)
	assert.Less(t, got.After, got.Before, "whole request shrinks")
	assert.Less(t, got.After, 8500, "target comes from failed model")
	// Adapter limits and unrelated current model budgets are not recovery inputs.
	state.Models["m"] = ModelState{Known: true, Budget: 1000000, Rate: state.Models["m"].Rate, Calibrated: true}
	state.MinB = 1000000
	assert.Equal(t, got, Recover(state, entries, index.ViewRef{}, failure, Settings{MinSavingTokens: 99999, ProtectRecentToolTokens: 99999}, true))
}

func TestRecoverNewestPreviewsAndForcedKeep(t *testing.T) {
	entries := []index.MessageEntry{
		{ID: 1, Kind: index.Assistant, Role: "assistant", Bytes: 50, ToolUseIDs: []string{"a", "b"}},
		{ID: 2, Kind: index.ToolResult, Role: "user", Bytes: 20000, Hash: "a", Keep: true, ToolUseIDs: []string{"a"}},
		{ID: 3, Kind: index.ToolResult, Role: "user", Bytes: 20000, Hash: "b", Keep: true, ToolUseIDs: []string{"b"}},
	}
	failure := contextmeasure.ContextOverflow{Failed: contextmeasure.RequestFacts{Model: "m", ViewBearing: true, TextBytes: 40050, ViewTextBytes: 40050}}
	preview := Recover(State{}, entries, index.ViewRef{}, failure, Settings{}, true)
	assert.Equal(t, Preview, preview.Tier)
	assert.True(t, preview.Reached, "uncalibrated recovery halves rendered bytes")
	assert.Equal(t, index.MessageID(3), preview.View.PreviewThrough)
	assert.Positive(t, preview.View.Budget.PreviewTokens)
	failure.Failed.TextBytes, failure.Failed.ViewTextBytes = 566, 566
	forced := Recover(State{}, entries, index.ViewRef{TailFrom: 1, PreviewThrough: 3, Budget: index.RenderBudget{PreviewTokens: MinPreviewBytes / 2}}, failure, Settings{}, true)
	assert.Equal(t, Placeholder, forced.Tier)
	assert.Equal(t, index.MessageID(3), forced.View.ForceClearThrough)
	assert.GreaterOrEqual(t, forced.View.PreviewThrough, preview.View.PreviewThrough)
	assert.True(t, Minimal(entries, forced.View))
	assert.True(t, entries[1].Keep)
}

func TestRecoverEvictionBudgetAndHalfTarget(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user", Bytes: 50000}, {ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 50000}, {ID: 3, Kind: index.Assistant, Role: "assistant", Bytes: 1000}}
	failure := contextmeasure.ContextOverflow{Failed: contextmeasure.RequestFacts{Model: "small", ViewBearing: true, ContextWindow: 8000, TextBytes: 101000, ViewTextBytes: 101000}}
	got := Recover(State{}, entries, index.ViewRef{Budget: index.RenderBudget{MaxSummaryTokens: 16000}}, failure, Settings{}, true)
	assert.Equal(t, Evict, got.Tier)
	assert.Equal(t, index.MessageID(3), got.View.TailFrom)
	assert.Equal(t, 800, got.View.Budget.MaxSummaryTokens)
	assert.True(t, got.Reached)
	assert.Less(t, got.After, got.Before)
}

// Decision 5 preserves default budgets and refuses unavailable recovery before
// changing the view. The workflow wraps this shortfall with the provider cause.
func TestRecoverSmallKnownBudgetIsLoadable(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user", Bytes: 30000}, {ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 30000}, {ID: 3, Kind: index.Assistant, Role: "assistant", Bytes: 30000}}
	view := index.ViewRef{}
	failure := contextmeasure.ContextOverflow{Source: "provider", Failed: contextmeasure.RequestFacts{Model: "small", ViewBearing: true, ContextWindow: 200, TextBytes: 90000, ViewTextBytes: 90000}}
	got := Recover(State{}, entries, view, failure, Settings{}, true)
	assert.Equal(t, view, got.View, "recovery is not attempted")
	assert.Equal(t, None, got.Tier)
	assert.False(t, got.Reached)
	if assert.NotNil(t, got.Shortfall) {
		assert.Equal(t, 200, got.Shortfall.Budget)
		assert.Equal(t, 472, got.Shortfall.MaxBytes)
		assert.Equal(t, 524, got.Shortfall.MinBytes)
		assert.ErrorIs(t, got.Shortfall, checkpoint.ErrCheckpointBudget)
		assert.Contains(t, got.Shortfall.Error(), "52 bytes short")
	}
}
