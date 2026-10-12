package compaction

import (
	"encoding/json/v2"
	"fmt"
	"reflect"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextOverrideValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override ContextOverrides
		field    string
	}{
		{"trigger zero", ContextOverrides{TriggerAt: new(Ratio{0, 100})}, "TriggerAt"},
		{"trigger one", ContextOverrides{TriggerAt: new(Ratio{1, 1})}, "TriggerAt"},
		{"trigger valid", ContextOverrides{TriggerAt: new(Ratio{99, 100})}, ""},
		{"denominator", ContextOverrides{TriggerAt: new(Ratio{1, 0})}, "TriggerAt"},
		{"margin zero", ContextOverrides{GuardMargin: new(Ratio{0, 1})}, ""},
		{"margin half", ContextOverrides{GuardMargin: new(Ratio{1, 2})}, ""},
		{"margin over", ContextOverrides{GuardMargin: new(Ratio{51, 100})}, "GuardMargin"},
		{"margin negative", ContextOverrides{GuardMargin: new(Ratio{-1, 10})}, "GuardMargin"},
		{"margin denominator", ContextOverrides{GuardMargin: new(Ratio{0, 0})}, "GuardMargin"},
		{"window", ContextOverrides{ContextWindow: new(0)}, "ContextWindow"},
		{"chunks", ContextOverrides{MaxSummaryChunks: new(65)}, "MaxSummaryChunks"},
		{"tier without threshold", ContextOverrides{MaxTier: new(Clear)}, "MaxTier"},
		{"tier invalid", ContextOverrides{Threshold: new(true), MaxTier: new(Tier("other"))}, "MaxTier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOverrides(Settings{}, &tc.override)
			if tc.field == "" {
				assert.NoError(t, err)
				return
			}
			var setting *ContextSettingsError
			require.ErrorAs(t, err, &setting)
			assert.Equal(t, tc.field, setting.Field)
		})
	}
	// Every absolute/count override must reject an explicit zero and negative value.
	typ := reflect.TypeFor[ContextOverrides]()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Type != reflect.TypeFor[*int]() {
			continue
		}
		for _, value := range []int{0, -1} {
			t.Run(fmt.Sprintf("%s=%d", field.Name, value), func(t *testing.T) {
				var overrides ContextOverrides
				reflect.ValueOf(&overrides).Elem().Field(i).Set(reflect.ValueOf(new(value)))
				var setting *ContextSettingsError
				require.ErrorAs(t, ValidateOverrides(Settings{}, &overrides), &setting)
				assert.Equal(t, field.Name, setting.Field)
			})
		}
	}
}

func TestContextValidateAt(t *testing.T) {
	// At B=10k the other default residents total 3300; framing=200.
	for _, tc := range []struct {
		name     string
		settings Settings
		b        int
		field    string
	}{
		{"resident equal", Settings{KeepRecentTokens: 1500}, 10000, ""},
		{"resident over", Settings{KeepRecentTokens: 1501}, 10000, "ResidentTokens"},
		{"checkpoint below", Settings{KeepUserTokens: 145, MaxSummaryTokens: 1, ReceiptTokens: 1, FramingTokens: 247}, 10000, "CheckpointBytes"},
		{"checkpoint equal", Settings{KeepUserTokens: 145, MaxSummaryTokens: 1, ReceiptTokens: 1, FramingTokens: 248}, 10000, ""},
		{"minimum", Settings{}, 2900, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAt(tc.settings, tc.b)
			if tc.field == "" {
				assert.NoError(t, err)
				return
			}
			var setting *ContextSettingsError
			require.ErrorAs(t, err, &setting)
			assert.Equal(t, tc.field, setting.Field)
			assert.Equal(t, tc.b, setting.B)
		})
	}
	b := RenderBudget(Settings{KeepUserTokens: 145, MaxSummaryTokens: 1, ReceiptTokens: 1, FramingTokens: 247}, 10000, 0)
	assert.Equal(t, checkpoint.MinCheckpointBytes(b)-2, checkpoint.MaxBytes(b))
}

func TestContextDefaultBudgets(t *testing.T) {
	for b := 2900; b < 250000; b += 37 {
		require.NoError(t, ValidateAt(Settings{}, b), "B=%d", b)
	}
}

func TestContextCanonical(t *testing.T) {
	base := Merge(Settings{}, nil)
	same := Merge(base, &ContextOverrides{TriggerAt: new(Ratio{170, 200}), GuardMargin: new(Ratio{2, 20}), RecallReadCalls: new(20)})
	assert.Equal(t, Canonical(base), Canonical(same))
	var decoded Settings
	require.NoError(t, DecodeCanonical(Canonical(base), &decoded))
	assert.Equal(t, base, decoded)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(Canonical(base), &wire))
	assert.InDelta(t, 1, wire["v"], 0)
	// The fixed-order canonical form must cover every effective setting field.
	typ := reflect.TypeFor[Settings]()
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			changed := base
			f := reflect.ValueOf(&changed).Elem().Field(i)
			switch value := f.Interface().(type) {
			case int:
				f.SetInt(int64(value + 1))
			case bool:
				f.SetBool(!value)
			case Tier:
				f.SetString(string(value) + "changed")
			case contextmeasure.Rate:
				f.Set(reflect.ValueOf(contextmeasure.Rate{Tokens: 1, Bytes: 3}))
			case SummaryConfig:
				value.Model.ID = "changed"
				changed.Summary = value
			case SummaryPrompt:
				value.Template = "changed"
				changed.Prompt = value
			default:
				t.Fatalf("uncovered canonical field %s", typ.Field(i).Name)
			}

			assert.NotEqual(t, Canonical(base), Canonical(changed))
		})
	}
}

func TestContextEffectiveBudget(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		window, reserve, pretend, want int
		invalid                        bool
	}{
		{"real", 200000, 1000, 0, 199000, false},
		{"pretend", 200000, 1000, 8000, 7000, false},
		{"unknown", 0, 1000, 8000, 7000, false},
		{"minimum", 200000, 1000, 3900, 2900, false},
		{"below minimum", 200000, 1000, 3899, 0, true},
		{"reserve", 200000, 8000, 8000, 0, true},
		{"above real", 8000, 1000, 9000, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EffectiveBudget(Settings{ContextWindow: tc.pretend}, contextmeasure.RequestFacts{Model: "model", ContextWindow: tc.window, OutputReserve: tc.reserve})
			if tc.invalid {
				var setting *ContextSettingsError
				require.ErrorAs(t, err, &setting)
				assert.Equal(t, "model", setting.Model)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestContextObserveAndRecoveryOverrides(t *testing.T) {
	settings := Merge(Settings{Threshold: true, ContextWindow: 8000}, nil)
	fact := contextmeasure.RequestFacts{Model: "m", ViewBearing: true, ContextWindow: 200000, OutputReserve: 1000, TextBytes: 60000, ViewTextBytes: 60000, InputTokens: 20000}
	state, err := ObserveEffective(State{}, []contextmeasure.RequestFacts{fact}, settings)
	require.NoError(t, err)
	assert.Equal(t, 7000, state.MinB)
	assert.Equal(t, 7000, state.Models["m"].Budget)
	view := index.ViewRef{Budget: RenderBudget(settings, 7000, 0)}
	recovery := Recover(state, nil, view, contextmeasure.ContextOverflow{Failed: fact}, settings, true)
	assert.Equal(t, view.Budget, recovery.View.Budget, "real recovery must not widen the pretend budget")
	settings.ContextWindow = 0
	settings.KeepRecentTokens = 10000
	_, err = ObserveEffective(State{ValidateBudgets: true}, []contextmeasure.RequestFacts{{Model: "large", ViewBearing: true, ContextWindow: 200000}, {Model: "small", ViewBearing: true, ContextWindow: 8000}}, settings)
	var setting *ContextSettingsError
	require.ErrorAs(t, err, &setting)
	assert.Equal(t, "small", setting.Model)
	assert.Equal(t, 8000, setting.B)
}

func TestContextTierClear(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: "user", Bytes: 40000}, {ID: 2, Kind: index.Message, Role: "assistant", Bytes: 40000}, {ID: 3, Kind: index.Message, Role: "user", Bytes: 40000}, {ID: 4, Kind: index.Message, Role: "assistant", Bytes: 40000}}
	state := Observe(State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 8000}})
	d := Decide(state, entries, index.ViewRef{}, Settings{MaxTier: Clear})
	assert.Zero(t, d.View.TailFrom)
	assert.NotEqual(t, Evict, d.Tier)
}

func TestContextCanonicalRulesGolden(t *testing.T) {
	const expected = `{"v":1,"max_tier":"summary","guard_margin":{"Tokens":1,"Bytes":10},"framing_tokens":200,"max_pins":32,"max_pinned_parts":2,"max_summary_chunks":8,"recall_scan_bytes":8000000,"TriggerAt":{"Tokens":17,"Bytes":20},"RecallDiscoveryCalls":20,"RecallReadCalls":20,"rules":{"keep_recent":{"ratio":{"Num":1,"Den":10},"cap":20000},"keep_user":{"ratio":{"Num":1,"Den":20},"cap":10000},"protect_recent_tool":{"ratio":{"Num":3,"Den":20},"cap":40000},"min_saving":{"ratio":{"Num":1,"Den":10},"cap":20000},"max_summary":{"ratio":{"Num":1,"Den":10},"cap":16000},"receipt":{"ratio":{"Num":3,"Den":100},"cap":6000},"max_summarised_result":{"ratio":{"Num":1,"Den":2}}}}`
	if string(Canonical(Settings{})) != expected {
		t.Fatalf("canonical bytes changed: %s", Canonical(Settings{}))
	}
}

func TestContextInvalidLimitsWithPretend(t *testing.T) {
	state, err := ObserveEffective(State{}, []contextmeasure.RequestFacts{{ViewBearing: true, Model: "invalid", LimitsInvalid: true}}, Settings{Threshold: true, ContextWindow: 8000})
	require.NoError(t, err)
	assert.Equal(t, 8000, state.MinB)
	assert.True(t, state.Flags.LimitsUnknown)
	assert.Equal(t, "invalid", state.InvalidLimitsModel)
}

func TestContextRecoveryInitialOverrideBudget(t *testing.T) {
	settings := Merge(Settings{ContextWindow: 8000, KeepUserTokens: 100, MaxSummaryTokens: 400, ReceiptTokens: 100}, nil)
	fact := contextmeasure.RequestFacts{Model: "m", ViewBearing: true, ContextWindow: 200000, OutputReserve: 1000}
	state, err := ObserveEffective(State{}, []contextmeasure.RequestFacts{fact}, settings)
	require.NoError(t, err)
	recovery := Recover(state, nil, index.ViewRef{}, contextmeasure.ContextOverflow{Failed: fact}, settings, true)
	assert.Equal(t, RenderBudget(settings, 7000, 0), recovery.View.Budget)
}

func TestContextValidationWithoutThreshold(t *testing.T) {
	_, err := ObserveEffective(State{ValidateBudgets: true}, []contextmeasure.RequestFacts{{ViewBearing: true, Model: "m", ContextWindow: 8000}}, Settings{KeepRecentTokens: 10000})
	var invalid *ContextSettingsError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "ResidentTokens", invalid.Field)
}

func TestContextCanonicalNestedFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Settings)
	}{
		{"enabled", func(s *Settings) { s.Summary.Enabled = true }},
		{"model ID", func(s *Settings) { s.Summary.Model.ID = "model" }},
		{"model class", func(s *Settings) { s.Summary.Model.Class = "small" }},
		{"images", func(s *Settings) { s.Summary.Model.AcceptsImages = true }},
		{"documents", func(s *Settings) { s.Summary.Model.AcceptsDocuments = true }},
		{"instructions", func(s *Settings) { s.Summary.Settings.Instructions = "instruction" }},
		{"baseline", func(s *Settings) { s.Summary.Settings.Prompt = "template" }},
		{"prompt ID", func(s *Settings) { s.Prompt.Ref.ID = "prompt" }},
		{"prompt version", func(s *Settings) { s.Prompt.Ref.Version = "version" }},
		{"template", func(s *Settings) { s.Prompt.Template = "hash" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := Merge(Settings{}, nil)
			changed := base
			tc.change(&changed)
			assert.NotEqual(t, Canonical(base), Canonical(changed))
		})
	}
}
