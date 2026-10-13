package compaction

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultQualificationRecordedFold(t *testing.T) {
	s := Settings{Default: true, KeepNonTextResident: true, MaxTier: Summary}
	assert.False(t, On(State{}, nil, s))
	for _, config := range []string{"anthropic", "bedrock-anthropic", "bedrock", "openai", "gemini", "ollama", "codex", ""} {
		assert.Empty(t, QualifiedTier(config, "claude-sonnet-4-20250514"))
	}
	first := contextmeasure.RequestFacts{Model: "a", ProviderConfig: "anthropic", Qualified: Summary, ViewBearing: true, ContextWindow: 200000, Call: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}}
	state := Observe(State{}, []contextmeasure.RequestFacts{first})
	assert.Equal(t, Summary, ObservedQualifiedTier(state))
	assert.Equal(t, first.Call, state.Models["a"].FirstCall)
	assert.Equal(t, "anthropic", state.Models["a"].ProviderConfig)
	assert.True(t, On(state, nil, s))
	state = Observe(state, []contextmeasure.RequestFacts{{Model: "side", Qualified: Clear}, {Model: "a", Qualified: Clear, ProviderConfig: "changed", ViewBearing: true}})
	assert.Equal(t, Summary, ObservedQualifiedTier(state), "repeated and non-view-bearing facts cannot rewrite qualification")
	state = Observe(state, []contextmeasure.RequestFacts{{Model: "b", Qualified: Evict, ViewBearing: true}})
	assert.Equal(t, Evict, ObservedQualifiedTier(state))
	assert.Equal(t, Evict, EffectiveTier(state, s))
	s.MaxTier = Clear
	assert.Equal(t, Clear, EffectiveTier(state, s))
	state = Observe(state, []contextmeasure.RequestFacts{{Model: "unqualified", ViewBearing: true}})
	assert.Empty(t, ObservedQualifiedTier(state))
	assert.False(t, On(state, nil, s))
	state = Observe(state, []contextmeasure.RequestFacts{{Model: "unqualified", Qualified: Summary, ViewBearing: true}})
	assert.False(t, On(state, nil, s), "first unqualified observation stays unqualified")
	s.Threshold = true
	assert.True(t, On(state, nil, s), "explicit requests do not require qualification")
}

func TestDefaultOverrideProvenance(t *testing.T) {
	registered := Settings{Default: true, KeepNonTextResident: true}
	disabled := Merge(registered, &ContextOverrides{Threshold: new(false)})
	assert.False(t, disabled.Default)
	assert.False(t, disabled.Threshold)
	explicit := Merge(registered, &ContextOverrides{Threshold: new(true)})
	assert.False(t, explicit.Default)
	assert.True(t, explicit.Threshold)
	require.Error(t, ValidateOverrides(registered, &ContextOverrides{KeepNonTextResident: new(false)}))
	require.NoError(t, ValidateOverrides(registered, &ContextOverrides{Threshold: new(true), KeepNonTextResident: new(false)}))
	explicit = Merge(registered, &ContextOverrides{Threshold: new(true), KeepNonTextResident: new(false)})
	assert.False(t, explicit.KeepNonTextResident)
	original := ContextOverrides{KeepNonTextResident: new(true)}
	owned := original.Clone()
	*original.KeepNonTextResident = false
	assert.True(t, *owned.KeepNonTextResident)
}

func TestNonTextResidentOriginalCoverage(t *testing.T) {
	support := model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, MaxImageBytes: 100}
	entry := make([]index.MessageEntry, 2, 3)
	entry[0] = index.MessageEntry{ID: 1, Kind: index.Message, NonText: 1500}
	entry[1] = index.MessageEntry{ID: 10, Kind: index.Message, Bytes: 100}
	settings := Settings{Default: true, KeepNonTextResident: true}
	cases := []struct {
		name            string
		state           State
		entries         []index.MessageEntry
		want            bool
		reason, witness string
	}{
		{"zero support", State{MinB: 20000, Models: map[string]ModelState{"z": {Qualified: Evict}, "a": {Qualified: Evict}}}, entry, true, "provider", "a"},
		{"aggregate too large", State{MinB: 10000, Models: map[string]ModelState{"large": {Budget: 20000, ToolResultParts: support}, "small": {Budget: 10000, ToolResultParts: support}}}, append(entry, index.MessageEntry{ID: 11, NonText: 1500}), true, "too_large", "small"},
		{"text only", State{MinB: 10000, Models: map[string]ModelState{"a": {}}}, []index.MessageEntry{{ID: 1, Bytes: 100}}, false, "", ""},
		{"carried placeholder", State{MinB: 10000, Models: map[string]ModelState{"a": {}}}, []index.MessageEntry{{ID: 1, Origin: &index.Origin{Scope: "old"}, NonText: 0}}, false, "", ""},
		{"sticky", State{Flags: Flags{NonTextResident: true}}, nil, true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason, witness := NonTextResident(tc.state, tc.entries, settings)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.reason, reason)
			assert.Equal(t, tc.witness, witness)
		})
	}
	explicit := Settings{Threshold: true}
	got, _, _ := NonTextResident(cases[0].state, entry, explicit)
	assert.False(t, got, "explicit waiver remains explicit")
	sticky := State{Flags: Flags{NonTextResident: true}, Models: map[string]ModelState{"a": {Qualified: Evict}}}
	assert.False(t, On(sticky, entry, settings))
	assert.True(t, On(sticky, entry, explicit))
}

func TestDefaultUnknownLimitsRemainNonfatal(t *testing.T) {
	fact := contextmeasure.RequestFacts{Model: "unknown", Qualified: Evict, ViewBearing: true}
	state, err := ObserveEffective(State{}, []contextmeasure.RequestFacts{fact}, Settings{Default: true})
	require.NoError(t, err)
	assert.True(t, state.Flags.LimitsUnknown)
	_, err = ObserveEffective(State{}, []contextmeasure.RequestFacts{fact}, Settings{Threshold: true})
	require.Error(t, err)
}

func TestDefaultQualificationExactPairs(t *testing.T) {
	qualifications := []Qualification{{Config: "bedrock-anthropic", Models: []string{"anthropic.claude-sonnet-4-20250514-v1:0"}, Tier: Evict}, {Config: "anthropic", Models: []string{"claude-sonnet-4-20250514"}, Tier: Summary}}
	for _, tc := range []struct {
		config, model string
		want          Tier
	}{
		{"anthropic", "claude-sonnet-4-20250514", Summary},
		{"anthropic", "claude-sonnet-4-20250515", ""},
		{"anthropic", "claude-sonnet-4", ""},
		{"unknown", "claude-sonnet-4-20250514", ""},
		{"", "claude-sonnet-4-20250514", ""},
		{"bedrock-anthropic", "us.anthropic.claude-sonnet-4-20250514-v1:0", Evict},
		{"bedrock-anthropic", "eu.anthropic.claude-sonnet-4-20250514-v1:0", Evict},
		{"bedrock-anthropic", "apac.anthropic.claude-sonnet-4-20250514-v1:0", Evict},
		{"bedrock", "us.anthropic.claude-sonnet-4-20250514-v1:0", ""},
	} {
		assert.Equal(t, tc.want, LookupQualification(qualifications, tc.config, tc.model), "%s/%s", tc.config, tc.model)
	}
}

func TestDefaultContinuationQualificationEveryStep(t *testing.T) {
	settings := Settings{Default: true}
	state, err := ObserveEffective(State{}, nil, settings)
	require.NoError(t, err)
	assert.True(t, state.Flags.UnqualifiedStep, "a no-model planner step cannot certify continuation")
	state, err = ObserveEffective(state, []contextmeasure.RequestFacts{{Model: "m", Qualified: Evict, ViewBearing: true, ContextWindow: 10000}}, settings)
	require.NoError(t, err)
	assert.True(t, state.Flags.UnqualifiedStep, "a later qualified step cannot erase earlier ineligibility")
	qualified, err := ObserveEffective(State{}, []contextmeasure.RequestFacts{{Model: "m", Qualified: Evict, ViewBearing: true, ContextWindow: 10000}}, settings)
	require.NoError(t, err)
	assert.False(t, qualified.Flags.UnqualifiedStep)
}

func TestNonTextBudgetWitnessSortedOnTies(t *testing.T) {
	state := Observe(State{}, []contextmeasure.RequestFacts{{Model: "z", ViewBearing: true, ContextWindow: 3000}, {Model: "a", ViewBearing: true, ContextWindow: 3000}})
	assert.Equal(t, "a", state.LimitingModel)
}

func TestNonTextResidencyRequiresObservedSupport(t *testing.T) {
	settings := Settings{Threshold: true, KeepNonTextResident: true}
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, NonText: 1500}}
	resident, reason, witness := NonTextResident(State{}, entries, settings)
	assert.False(t, resident)
	assert.Empty(t, reason)
	assert.Empty(t, witness)
}
