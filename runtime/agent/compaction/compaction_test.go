package compaction

import (
	jsonopts "encoding/json"
	"encoding/json/v2"
	"math"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObserveRecordedFacts(t *testing.T) {
	facts := []contextmeasure.RequestFacts{
		{Model: "ignored", ViewBearing: false, ContextWindow: 2000},
		{Model: "z", ViewBearing: true, ContextWindow: 12000, OutputReserve: 2000, InputTokens: 3000, TextBytes: 9000, FixedBytes: 30},
		{Model: "z", ViewBearing: true, ContextWindow: 12000, OutputReserve: 2000, FixedBytes: 60},
		{Model: "a", ViewBearing: true, ContextWindow: 8000, OutputReserve: 1000},
		{Model: "unknown", ViewBearing: true},
	}
	state := Observe(State{}, facts)
	assert.Len(t, state.Models, 3)
	assert.Equal(t, 7000, state.MinB)
	assert.Equal(t, contextmeasure.Rate{Tokens: 1, Bytes: 3}, state.Models["z"].Rate)
	assert.True(t, state.Models["z"].Calibrated)
	assert.Equal(t, 60, state.Models["z"].FixedBytes)
	assert.False(t, state.Models["unknown"].Known)
	assert.True(t, state.Flags.LimitsUnknown)
	next := Observe(state, []contextmeasure.RequestFacts{{Model: "a", ViewBearing: true, ContextWindow: 20000}})
	assert.Equal(t, 7000, next.MinB)
	assert.Equal(t, 7000, state.Models["a"].Budget)
	small := Observe(next, []contextmeasure.RequestFacts{{Model: "small", ViewBearing: true, ContextWindow: 2899}})
	assert.True(t, small.Flags.BudgetTooSmall)
	assert.True(t, Observe(small, []contextmeasure.RequestFacts{{Model: "small", ViewBearing: true, ContextWindow: 9000}}).Flags.BudgetTooSmall)
	assert.True(t, Observe(State{}, facts[:1]).Flags.Unmeasured)
}

func TestDecideTriggerAndTierOne(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fixed, saving int
		keep          bool
		tier          Tier
		cleared       index.MessageID
	}{
		{name: "boundary", fixed: 15000, saving: 300, tier: None},
		{name: "clear", fixed: 24000, saving: 6000, tier: Clear, cleared: 3},
		{name: "below minimum", fixed: 27000, saving: 300, tier: Evict},
		{name: "keep", fixed: 27000, saving: 6000, keep: true, tier: Evict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user"}, {ID: 2, Kind: index.Assistant, Role: "assistant", ToolUseIDs: []string{"old"}}, {ID: 3, Kind: index.ToolResult, Role: "user", Bytes: tc.saving, Keep: tc.keep, ToolUseIDs: []string{"old"}}, {ID: 4, Kind: index.Assistant, Role: "assistant", ToolUseIDs: []string{"new"}}, {ID: 5, Kind: index.ToolResult, Role: "user", Bytes: 300, ToolUseIDs: []string{"new"}}}
			state := knownState(tc.fixed)
			decision := Decide(state, entries, index.ViewRef{}, Settings{KeepRecentTokens: 20000, ProtectRecentToolTokens: 100, MinSavingTokens: 1000})
			assert.Equal(t, tc.tier, decision.Tier)
			assert.Equal(t, tc.cleared, decision.View.ClearedThrough)
			if tc.tier == Evict {
				assert.Equal(t, index.MessageID(2), decision.View.TailFrom)
			} else {
				assert.Zero(t, decision.View.TailFrom)
			}
		})
	}
}

func TestDecideNoProgress(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user", Bytes: 30}, {ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 30}}
	settings := Settings{MinSavingTokens: 1000}
	first := Decide(knownState(30000), entries, index.ViewRef{}, settings)
	assert.Equal(t, Evict, first.Tier)
	assert.Equal(t, first.After, first.State.NoProgressAt)
	again := Decide(first.State, entries, first.View, settings)
	assert.Equal(t, NoProgress, again.Skipped)
	assert.Equal(t, first.State.NoProgressAt, again.State.NoProgressAt)
	entries[1].Bytes += 2999
	below := Decide(first.State, entries, first.View, settings)
	assert.Equal(t, first.State.NoProgressAt, below.State.NoProgressAt)
	entries[1].Bytes++
	grown := Decide(first.State, entries, first.View, settings)
	assert.Equal(t, first.State.NoProgressAt+1000, grown.State.NoProgressAt)
}

func TestDecideCoverageDeterminismAndMonotonicity(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user", Bytes: 300}, {ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 30000, ToolUseIDs: []string{"a"}}, {ID: 3, Kind: index.ToolResult, Role: "user", Bytes: 30000, ToolUseIDs: []string{"a"}}, {ID: 4, Kind: index.Assistant, Role: "assistant", Bytes: 300}, {ID: 5, Kind: index.Clarification, Role: "user", Bytes: 300}, {ID: 6, Kind: index.Assistant, Role: "assistant", Bytes: 300}}
	data, err := json.Marshal(entries)
	require.NoError(t, err)
	settings := Settings{KeepRecentTokens: 500, ProtectRecentToolTokens: 20000, MinSavingTokens: 1000}
	state := knownState(0)
	view := index.ViewRef{Pinned: []index.MessageID{1}}
	got := Decide(state, entries, view, settings)
	assert.Equal(t, Evict, got.Tier)
	assert.Equal(t, index.MessageID(4), got.View.TailFrom)
	assert.Contains(t, checkpoint.CutPoints(checkpoint.Units(entries, index.RecordedRole, 0)), got.View.TailFrom)
	assert.Equal(t, data, mustJSON(t, entries))
	assert.Equal(t, index.MessageID(0), view.TailFrom)
	assert.Equal(t, 0, state.NoProgressAt)
	var restored []index.MessageEntry
	require.NoError(t, json.Unmarshal(data, &restored))
	replay := Decide(knownState(0), restored, view, settings)
	assert.Equal(t, got, replay)
	next := Decide(got.State, entries, got.View, settings)
	assert.GreaterOrEqual(t, next.View.TailFrom, got.View.TailFrom)
	assert.GreaterOrEqual(t, next.View.ClearedThrough, got.View.ClearedThrough)
	state.Models["a"] = state.Models["model"]
	reverse := State{Models: map[string]ModelState{"a": state.Models["a"], "model": state.Models["model"]}, MinB: state.MinB, EffectiveWindow: state.EffectiveWindow, LimitingModel: state.LimitingModel}
	assert.Equal(t, Decide(state, entries, view, settings), Decide(reverse, entries, view, settings))
	state.OpenFrom = 2
	assert.Equal(t, index.MessageID(2), Decide(state, entries, view, settings).View.TailFrom)
}

func TestDecideBudgetAndFirstStep(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user", Bytes: 90000}, {ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 90000}}
	assert.Equal(t, index.ViewRef{}, Decide(State{}, entries, index.ViewRef{}, Settings{}).View)
	got := Decide(knownState(0), nil, index.ViewRef{}, Settings{})
	assert.Equal(t, index.RenderBudget{KeepUserTokens: 500, MaxSummaryTokens: 1000, ReceiptTokens: 300, FramingTokens: 200}, got.View.Budget)
	shrunk := Observe(got.State, []contextmeasure.RequestFacts{{Model: "small", ViewBearing: true, ContextWindow: 5000}})
	next := Decide(shrunk, nil, got.View, Settings{})
	assert.Equal(t, index.RenderBudget{KeepUserTokens: 250, MaxSummaryTokens: 500, ReceiptTokens: 150, FramingTokens: 200}, next.View.Budget)
	off := Observe(shrunk, []contextmeasure.RequestFacts{{Model: "tiny", ViewBearing: true, ContextWindow: 2899}})
	assert.Equal(t, None, Decide(off, entries, next.View, Settings{}).Tier)
}

func knownState(fixed int) State {
	return Observe(State{}, []contextmeasure.RequestFacts{{Model: "model", ViewBearing: true, ContextWindow: 10000, FixedBytes: fixed}})
}
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value, jsonopts.FormatDurationAsNano(true))
	require.NoError(t, err)
	return data
}

func TestDecideRationalBoundaryAndRecordedSizes(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user"}, {ID: 2, Kind: index.Assistant, Role: "assistant", Bytes: 25500}}
	at := Decide(knownState(0), entries, index.ViewRef{}, Settings{})
	assert.Equal(t, None, at.Tier)
	assert.Equal(t, 8500, at.Before)
	entries[1].Bytes++
	over := Decide(knownState(0), entries, index.ViewRef{}, Settings{})
	assert.Equal(t, Evict, over.Tier)
	// The equivalent of UnsafeUnrecordedSize: content hashes and server evidence
	// may point to differently encoded bodies; only recorded measurements count.
	entries[0].Hash = "different-content"
	entries[1].Hash = "larger-encoding"
	entries[1].Record = []runstore.Hash{"server-only"}
	assert.Equal(t, over, Decide(knownState(0), entries, index.ViewRef{}, Settings{}))
	// The equivalent of UnsafeUnrecordedLimit: serialization of facts/settings is
	// the authority; no provider configuration is an input to reconstruction.
	facts := []contextmeasure.RequestFacts{{Model: "model", ViewBearing: true, ContextWindow: 10000}}
	var restoredFacts []contextmeasure.RequestFacts
	require.NoError(t, json.Unmarshal(mustJSON(t, facts), &restoredFacts, jsonopts.FormatDurationAsNano(true)))
	var restoredSettings Settings
	require.NoError(t, json.Unmarshal(mustJSON(t, Settings{}), &restoredSettings))
	assert.Equal(t, over, Decide(Observe(State{}, restoredFacts), entries, index.ViewRef{}, restoredSettings))
}

func TestDecideTierOneBelowMinimumAndKeep(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Assistant, Role: "assistant", Bytes: 30, ToolUseIDs: []string{"old", "kept", "new"}}, {ID: 2, Kind: index.ToolResult, Role: "user", Bytes: 300, ToolUseIDs: []string{"old"}}, {ID: 3, Kind: index.ToolResult, Role: "user", Bytes: 6000, Keep: true, ToolUseIDs: []string{"kept"}}, {ID: 4, Kind: index.ToolResult, Role: "user", Bytes: 300, ToolUseIDs: []string{"new"}}}
	settings := Settings{ProtectRecentToolTokens: 100, MinSavingTokens: 1000}
	below := Decide(knownState(27000), entries, index.ViewRef{}, settings)
	assert.Equal(t, None, below.Tier)
	assert.Equal(t, BelowMinSaving, below.Skipped)
	assert.Zero(t, below.View.ClearedThrough)
	entries[1].Bytes = 6000
	cleared := Decide(knownState(27000), entries, index.ViewRef{}, settings)
	assert.Equal(t, Clear, cleared.Tier)
	assert.Equal(t, index.MessageID(2), cleared.View.ClearedThrough)
	assert.True(t, entries[2].Keep)
}

func TestDecideSaturatedNoProgressAndCappedBudgets(t *testing.T) {
	state := knownState(math.MaxInt)
	entries := []index.MessageEntry{{ID: 1, Kind: index.Assistant, NonText: math.MaxInt}}
	first := Decide(state, entries, index.ViewRef{}, Settings{})
	assert.Equal(t, math.MaxInt, first.State.NoProgressAt)
	next := Decide(first.State, entries, first.View, Settings{})
	assert.Equal(t, NoProgress, next.Skipped)
	huge := Observe(State{}, []contextmeasure.RequestFacts{{Model: "huge", ViewBearing: true, ContextWindow: math.MaxInt}})
	got := Decide(huge, nil, index.ViewRef{}, Settings{})
	assert.Equal(t, index.RenderBudget{KeepUserTokens: 10000, MaxSummaryTokens: 16000, ReceiptTokens: 6000, FramingTokens: 200}, got.View.Budget)
}
