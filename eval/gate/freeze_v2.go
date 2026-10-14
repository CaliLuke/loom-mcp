package gate

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
)

// RoleIdentity binds the explicit raw provider ID and its normalized model ID.
type RoleIdentity struct {
	Provider   string `json:"provider"`
	Raw        string `json:"raw"`
	Normalized string `json:"normalized"`
}

// CapabilityProfile identifies the configured model capabilities without
// constructing a provider. Empty Parts explicitly means no non-text support.
type CapabilityProfile struct {
	Version string         `json:"version"`
	Models  []RoleIdentity `json:"models"`
	Parts   []string       `json:"parts"`
}

// verifyV2 validates policy structure independently of its content hash. Inputs
// are resolved by the caller from the current source and recorded evidence.
func (f Freeze) verifyV2(in FreezeInputs) error {
	if f.Version == 1 {
		return fmt.Errorf("gate: historical freeze v1 is unsupported for v2 verification; use its retained source")
	}
	if f.Format != FreezeFormat || f.Version != 2 || f.Configuration == "" || f.SimulatorVersion != SimulatorVersion || f.MaterializerVersion == "" || f.Created.IsZero() {
		return fmt.Errorf("gate: unsupported freeze contract")
	}
	if f.Margins != (Margins{}) || f.Alpha != 0 || len(f.Looks) > 0 || f.Repeats != 0 || f.Bootstrap != (Bootstrap{}) || f.Guard != 0 {
		return fmt.Errorf("gate: v2 has conflicting legacy policy fields")
	}
	if err := f.verifySelections(); err != nil {
		return err
	}
	for _, pair := range [][2]string{{f.SourceSHA256, in.SourceSHA256}, {f.SourceBundleSHA256, in.SourceBundleSHA256}, {f.ValidationSHA256, in.ValidationSHA256}, {f.InventorySHA256, in.InventorySHA256}, {f.CalibrationSHA256, in.CalibrationSHA256}} {
		digest, err := hex.DecodeString(pair[0])
		if err != nil || len(digest) != 32 || pair[0] != pair[1] {
			return fmt.Errorf("gate: missing or mismatched provenance digest")
		}
	}
	if err := f.verifyProfiles(in); err != nil {
		return err
	}
	if len(f.EffectivePolicies) == 0 || !reflect.DeepEqual(f.EffectivePolicies, in.EffectivePolicies) {
		return fmt.Errorf("gate: effective policy mismatch")
	}
	for _, raw := range f.EffectivePolicies {
		if !json.Valid(raw) || string(raw) == "null" {
			return fmt.Errorf("gate: malformed effective policy")
		}
	}
	return nil
}

func validRole(role RoleIdentity) bool {
	return role.Provider != "" && role.Raw != "" && role.Normalized != ""
}

// verifySelections binds eligibility to the selected policy margins.
func (f Freeze) verifySelections() error {
	variants := make([]string, 0, 2)
	if len(f.Selections) != 2 {
		return fmt.Errorf("gate: missing candidate selections")
	}
	seen := map[string]bool{}
	for _, v := range f.Selections {
		if (v.Variant != receiptVariant && v.Variant != hybridVariant) || seen[v.Variant] {
			return fmt.Errorf("gate: invalid candidate selection identity")
		}
		seen[v.Variant] = true
		switch {
		case v.Status == CalibrationIneligible:
			if v.Margin != 0 {
				return fmt.Errorf("gate: ineligible variant has a selected margin")
			}
		case v.Status == CalibrationEligible:
			if err := verifySelectedCandidate(v, f.Policy); err != nil {
				return err
			}
			variants = append(variants, v.Variant)
		default:
			return fmt.Errorf("gate: unsupported candidate eligibility")
		}
	}
	if len(variants) == 0 || f.Policy == nil || len(f.Policy.Unsupported) != len(variants) {
		return fmt.Errorf("gate: freeze must contain an eligible variant")
	}
	if err := f.Policy.Validate(variants); err != nil {
		return err
	}
	return nil
}

// verifyProfiles checks each role and scenario capability identity.
func (f Freeze) verifyProfiles(in FreezeInputs) error {
	if len(f.Roles) != 3 || !reflect.DeepEqual(f.Roles, in.Roles) {
		return fmt.Errorf("gate: role identity mismatch")
	}
	for _, role := range []string{"planner", "planner_small", "summariser"} {
		if !validRole(f.Roles[role]) {
			return fmt.Errorf("gate: missing role identity %s", role)
		}
	}
	if len(f.GatedScenarios) == 0 || len(f.Profiles) == 0 || !reflect.DeepEqual(f.Profiles, in.Profiles) {
		return fmt.Errorf("gate: missing or mismatched capability profiles")
	}
	for _, scenario := range append(slices.Clone(f.GatedScenarios), f.ReportedScenarios...) {
		profile, ok := f.Profiles[scenario]
		if !ok || profile.Version != "v1" || len(profile.Models) == 0 {
			return fmt.Errorf("gate: missing profile %s", scenario)
		}
		for _, role := range profile.Models {
			if !validRole(role) {
				return fmt.Errorf("gate: malformed profile model identity")
			}
		}
	}
	return nil
}

// verifySelectedCandidate requires measured null power and every boundary.
func verifySelectedCandidate(v CandidateSelection, policy *Policy) error {
	if policy == nil || policy.Unsupported[v.Variant] != v.Margin || v.WorstNull < .8 || v.WorstNull > 1 || math.IsNaN(v.WorstNull) || len(v.WorstBoundary) != 4 {
		return fmt.Errorf("gate: invalid selected candidate evidence")
	}
	for _, criterion := range []string{measureSuccess, measureUnsupported, measureEvidence, measureRecall} {
		p, ok := v.WorstBoundary[criterion]
		if !ok || p < 0 || p > .05 || math.IsNaN(p) {
			return fmt.Errorf("gate: selected candidate fails boundary %s", criterion)
		}
	}
	return nil
}
