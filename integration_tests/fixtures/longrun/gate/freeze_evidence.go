package suite

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"example.com/longrun"
	"example.com/longrun/env"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

type freezeEvidence struct {
	source      SourceManifest
	validation  Validation
	calibration CalibrationArtifact
	inputs      gate.FreezeInputs
	pools       []gate.Pool
}

// BuildVerifiedFreeze authorizes only eligible candidates with complete current
// source, validation, calibration and declared historical-store evidence.
func BuildVerifiedFreeze(ctx context.Context, dir, configuration, inventoryPath string) (gate.Freeze, error) {
	if inventoryPath == "" {
		return gate.Freeze{}, fmt.Errorf("freeze requires an explicit inventory")
	}
	inventory, err := readMigrationInventory(dir, configuration, inventoryPath)
	if err != nil {
		return gate.Freeze{}, err
	}
	evidence, err := readFreezeEvidence(ctx, dir, configuration)
	if err != nil {
		return gate.Freeze{}, err
	}
	if evidence.inputs.InventorySHA256 != inventory.SHA256 {
		return gate.Freeze{}, fmt.Errorf("freeze inventory differs from the retained declaration")
	}
	if err := VerifyEvidenceExecutable(ctx, dir); err != nil {
		return gate.Freeze{}, err
	}
	return buildEvidenceFreeze(configuration, evidence)
}

func readFreezeEvidence(ctx context.Context, dir, configuration string) (freezeEvidence, error) {
	var out freezeEvidence
	var err error
	out.source, err = ReadSource(ctx, dir, configuration)
	if err != nil {
		return out, err
	}
	out.validation, err = ReadValidationFor(dir, configuration)
	if err != nil {
		return out, err
	}
	if out.validation.SourceSHA256 != out.source.Identity.SHA256 {
		return out, fmt.Errorf("validation source identity mismatch")
	}
	validationBytes, err := readFile(dir, filepath.Join("gate", filepath.Base(ValidationPath(dir, configuration))))
	if err != nil {
		return out, err
	}
	calibrationBytes, err := readFile(dir, filepath.Join("gate", filepath.Base(CalibrationPath(dir, configuration))))
	if err != nil {
		return out, err
	}
	if err := decodeArtifact(calibrationBytes, &out.calibration); err != nil {
		return out, err
	}
	cfg := CalibrationConfig(configuration, out.validation, out.calibration.Config.Workers)
	if out.calibration.Pilot != nil {
		cfg.Pilot, err = VerifyPilot(dir, configuration, *out.calibration.Pilot, out.validation.Profile, out.source.Identity.SHA256)
		if err != nil {
			return out, err
		}
	}
	if out.calibration.Configuration != configuration {
		return out, fmt.Errorf("calibration configuration mismatch")
	}
	if err := VerifyCalibration(out.calibration, cfg, out.source, gate.Digest(validationBytes)); err != nil {
		return out, err
	}
	inventoryBytes, err := readFile(dir, "gate/inventory_"+configuration+".json")
	if err != nil {
		return out, err
	}
	var inventory StoreInventory
	if err := decodeArtifact(inventoryBytes, &inventory); err != nil {
		return out, err
	}
	checked, err := finishInventory(inventory)
	if err != nil {
		return out, err
	}
	if inventory.Version != 1 || inventory.Configuration != configuration || !inventory.DeclaredComplete || inventory.Unknown || inventory.Consumed || inventory.SHA256 != checked.SHA256 {
		return out, fmt.Errorf("invalid retained migration inventory")
	}
	out.inputs, out.pools, err = Inputs(dir)
	if err != nil {
		return out, err
	}
	out.inputs.SourceSHA256 = out.source.Identity.SHA256
	out.inputs.SourceBundleSHA256 = out.source.BundleSHA256
	out.inputs.ValidationSHA256 = gate.Digest(validationBytes)
	out.inputs.CalibrationSHA256 = gate.Digest(calibrationBytes)
	out.inputs.InventorySHA256 = inventory.SHA256
	out.inputs.Roles = out.validation.Profile.Roles
	out.inputs.Profiles = freezeProfiles(out.validation.Profile)
	out.inputs.EffectivePolicies = out.validation.EffectivePolicies
	return out, nil
}

func buildEvidenceFreeze(configuration string, evidence freezeEvidence) (gate.Freeze, error) {
	margins := map[string]float64{}
	for _, selection := range evidence.calibration.Selections {
		if selection.Status == gate.CalibrationEligible {
			margins[selection.Variant] = selection.Margin
		}
	}
	if len(margins) == 0 {
		return gate.Freeze{}, fmt.Errorf("no calibration-eligible variant; retain the complete diagnostic calibration without an authorizing freeze")
	}
	policy := gate.ProposedPolicy(margins, BootstrapSeed)
	contexts, err := scenarioContexts(configuration)
	if err != nil {
		return gate.Freeze{}, err
	}
	policies := map[string]json.RawMessage{}
	for _, variant := range scenarios.Variants() {
		raw, err := jsonv2.Marshal(variant.Policy, jsonv2.Deterministic(true))
		if err != nil {
			return gate.Freeze{}, err
		}
		policies[variant.ID] = raw
	}
	in := evidence.inputs
	f := gate.Freeze{Format: gate.FreezeFormat, Version: 2, Configuration: configuration, Policy: &policy, Selections: evidence.calibration.Selections, Roles: in.Roles, Profiles: in.Profiles, EffectivePolicies: in.EffectivePolicies, SourceSHA256: in.SourceSHA256, SourceBundleSHA256: in.SourceBundleSHA256, ValidationSHA256: in.ValidationSHA256, InventorySHA256: in.InventorySHA256, CalibrationSHA256: in.CalibrationSHA256, SimulatorVersion: gate.SimulatorVersion, MaterializerVersion: env.MaterializerVersion, GatedScenarios: env.GatedScenarios(configuration), ReportedScenarios: env.ReportedScenarios(), ScenarioContexts: contexts, Policies: policies, Pools: evidence.pools, GitRevision: evidence.source.BaseRevision, Dirty: evidence.source.Dirty, Created: time.Now().UTC()}
	hash, err := f.Hash()
	if err != nil {
		return f, err
	}
	return f, f.Verify(hash, in)
}

func freezeProfiles(profile longrun.ConfigurationProfile) map[string]gate.CapabilityProfile {
	out := map[string]gate.CapabilityProfile{}
	for _, scenario := range env.ScenarioIDs(profile.Configuration) {
		models := []gate.RoleIdentity{profile.Roles["planner"]}
		support := profile.Supports["planner"]
		if scenario == "model_change" {
			models = append(models, profile.Roles["planner_small"])
			support = support.Intersect(profile.Supports["planner_small"])
		}
		parts := make([]string, 0, len(support.ImageFormats)+len(support.DocumentFormats))
		for _, format := range support.ImageFormats {
			parts = append(parts, "image/"+string(format))
		}
		for _, format := range support.DocumentFormats {
			parts = append(parts, "document/"+string(format))
		}
		out[scenario] = gate.CapabilityProfile{Version: "v1", Models: models, Parts: parts}
	}
	return out
}

// VerifyLiveProfile resolves explicit local role choices without provider I/O.
func VerifyLiveProfile(configuration string, expected map[string]gate.RoleIdentity) error {
	profile, err := longrun.ResolveProfile(configuration)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(profile.Roles, expected) {
		return fmt.Errorf("configured role identities differ from frozen evidence")
	}
	return nil
}
