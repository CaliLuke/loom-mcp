package gate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func v2FreezeFixture() (Freeze, FreezeInputs) {
	policy := ProposedPolicy(map[string]float64{"receipt_only": .02, "hybrid": .03}, 42)
	roles := map[string]RoleIdentity{
		"planner":       {Provider: "fake", Raw: "reader", Normalized: "reader"},
		"planner_small": {Provider: "fake", Raw: "small", Normalized: "small"},
		"summariser":    {Provider: "fake", Raw: "summary", Normalized: "summary"},
	}
	profiles := map[string]CapabilityProfile{"a": {Version: "v1", Models: []RoleIdentity{roles["planner"]}, Parts: []string{"image", "document"}}}
	effective := map[string]json.RawMessage{"a/receipt_only": json.RawMessage(`{"Threshold":true}`), "a/hybrid": json.RawMessage(`{"Threshold":true}`)}
	f := Freeze{Format: FreezeFormat, Version: 2, Configuration: "offline", Policy: &policy,
		Selections: []CandidateSelection{{Variant: "receipt_only", Margin: .02, Status: CalibrationEligible, WorstNull: .9, WorstBoundary: map[string]float64{"success": .01, "unsupported": .01, "evidence": .01, "recall": .01}}, {Variant: "hybrid", Margin: .03, Status: CalibrationEligible, WorstNull: .9, WorstBoundary: map[string]float64{"success": .01, "unsupported": .01, "evidence": .01, "recall": .01}}},
		Roles:      roles, Profiles: profiles, EffectivePolicies: effective,
		SourceSHA256: Digest([]byte("source")), SourceBundleSHA256: Digest([]byte("bundle")), ValidationSHA256: Digest([]byte("validation")), InventorySHA256: Digest([]byte("inventory")), CalibrationSHA256: Digest([]byte("calibration")),
		GatedScenarios: []string{"a"}, ReportedScenarios: []string{"a"}, MaterializerVersion: "v1", SimulatorVersion: SimulatorVersion, GitRevision: "base", Created: time.Unix(1, 0),
		Pools: []Pool{{Scenario: "a", Partition: "look1", DatasetHash: Digest([]byte("pool")), Items: []FrozenItem{{ID: "one", SHA256: Digest([]byte("item"))}}}},
	}
	in := FreezeInputs{Pools: map[string][]byte{"a/look1": []byte("pool")}, Materialized: map[string][]byte{"one": []byte("item")}, MaterializerVersion: "v1", SourceSHA256: f.SourceSHA256, SourceBundleSHA256: f.SourceBundleSHA256, ValidationSHA256: f.ValidationSHA256, InventorySHA256: f.InventorySHA256, CalibrationSHA256: f.CalibrationSHA256, Roles: roles, Profiles: profiles, EffectivePolicies: effective}
	return f, in
}

func TestK19cFreezeV2BindsPolicyAndProvenance(t *testing.T) {
	f, in := v2FreezeFixture()
	hash, err := f.Hash()
	require.NoError(t, err)
	require.NoError(t, f.Verify(hash, in))
	for _, tc := range []struct {
		name   string
		change func(*FreezeInputs)
	}{
		{"source", func(i *FreezeInputs) { i.SourceSHA256 = Digest([]byte("changed")) }},
		{"bundle", func(i *FreezeInputs) { i.SourceBundleSHA256 = "" }},
		{"validation", func(i *FreezeInputs) { i.ValidationSHA256 = "" }},
		{"inventory", func(i *FreezeInputs) { i.InventorySHA256 = "" }},
		{"calibration", func(i *FreezeInputs) { i.CalibrationSHA256 = "" }},
		{"roles", func(i *FreezeInputs) { i.Roles = nil }},
		{"capability", func(i *FreezeInputs) { i.Profiles = nil }},
		{"effective policy", func(i *FreezeInputs) { i.EffectivePolicies = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := in
			tc.change(&changed)
			require.Error(t, f.Verify(hash, changed))
		})
	}
}

func TestK19cFreezeRejectsMalformedV2AndHistoricalV1(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Freeze)
	}{
		{"historical", func(f *Freeze) { f.Version = 1 }},
		{"missing policy", func(f *Freeze) { f.Policy = nil }},
		{"missing selection", func(f *Freeze) { f.Selections = nil }},
		{"unsupported margin", func(f *Freeze) { f.Policy.Unsupported["hybrid"] = .04 }},
		{"selection mismatch", func(f *Freeze) { f.Selections[1].Margin = .05 }},
		{"missing source", func(f *Freeze) { f.SourceSHA256 = "" }},
		{"missing role", func(f *Freeze) { delete(f.Roles, "planner_small") }},
		{"missing capability", func(f *Freeze) { f.Profiles = nil }},
		{"missing effective policy", func(f *Freeze) { f.EffectivePolicies = nil }},
		{"legacy policy conflict", func(f *Freeze) { f.Alpha = .05 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, in := v2FreezeFixture()
			tc.change(&f)
			hash, err := f.Hash()
			require.NoError(t, err)
			require.Error(t, f.Verify(hash, in), "a self-consistent hash cannot authorize malformed policy or provenance")
		})
	}
}

func TestK19cHistoricalFreezeCannotAuthorizeV2(t *testing.T) {
	f := Freeze{Format: FreezeFormat, Version: 1, Configuration: "offline", Margins: DefaultMargins(), Alpha: .05, Looks: []int{12, 24, 48}, Repeats: 2, Bootstrap: Bootstrap{Resamples: 10000, Seed: 42}, Guard: -.10, MaterializerVersion: "v1"}
	hash, err := f.Hash()
	require.NoError(t, err)
	require.ErrorContains(t, f.Verify(hash, FreezeInputs{MaterializerVersion: "v1"}), "historical")
}
