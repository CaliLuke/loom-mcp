package suite

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"example.com/longrun/env"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// BootstrapSeed is fixed before calibration and all held-out looks.
const BootstrapSeed uint64 = 190017

// BuildFreeze binds the exact input bytes and assumed-rate calibration.
func BuildFreeze(dir, config string, calibration []byte, source eval.VCS) (gate.Freeze, error) {
	_, pools, err := Inputs(dir)
	if err != nil {
		return gate.Freeze{}, err
	}
	policies := map[string]json.RawMessage{}
	for _, v := range scenarios.Variants() {
		b, err := jsonv2.Marshal(v.Policy, jsonv2.Deterministic(true))
		if err != nil {
			return gate.Freeze{}, err
		}
		policies[v.ID] = b
	}
	contexts, err := scenarioContexts(config)
	if err != nil {
		return gate.Freeze{}, err
	}
	return gate.Freeze{ScenarioContexts: contexts, Dirty: source.Modified, Format: gate.FreezeFormat, Version: 1, Configuration: config, Margins: gate.DefaultMargins(), Alpha: .05, Looks: []int{12, 24, 48}, Repeats: 2, Bootstrap: gate.Bootstrap{Resamples: 10000, Seed: BootstrapSeed}, Guard: -.10, GatedScenarios: env.GatedScenarios(config), ReportedScenarios: env.ReportedScenarios(), Policies: policies, Pools: pools, MaterializerVersion: env.MaterializerVersion, CalibrationSHA256: gate.Digest(calibration), SimulatorVersion: gate.SimulatorVersion, GitRevision: source.Revision, Created: time.Now().UTC()}, nil
}

// WriteFreeze persists a write-once contract and a separate trusted hash sidecar.
func WriteFreeze(dir string, f gate.Freeze) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	hash, err := f.Hash()
	if err != nil {
		return err
	}
	path := freezePath(dir, f.Configuration)
	if err := writeOnce(path, append(data, '\n')); err != nil {
		return err
	}
	return writeOnce(strings.TrimSuffix(path, ".json")+".sha256", []byte(hash+"\n"))
}

// VerifyFreeze is mandatory before every look and report, including stored-only reports.
func VerifyFreeze(dir, config string) (gate.Freeze, error) {
	path := freezePath(dir, config)
	var f gate.Freeze
	data, err := readFile(dir, filepath.Join("gate", filepath.Base(path)))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, err
	}
	hash, err := readFile(dir, filepath.Join("gate", strings.TrimSuffix(filepath.Base(path), ".json")+".sha256"))
	if err != nil {
		return f, err
	}
	in, _, err := Inputs(dir)
	if err != nil {
		return f, err
	}
	if err := f.Verify(strings.TrimSpace(string(hash)), in); err != nil {
		return f, err
	}
	calibration, err := readFile(dir, filepath.Join("gate", filepath.Base(CalibrationPath(dir, config))))
	if err != nil {
		return f, err
	}
	if gate.Digest(calibration) != f.CalibrationSHA256 {
		return f, fmt.Errorf("calibration report hash mismatch")
	}
	if f.Configuration != config {
		return f, fmt.Errorf("freeze configuration mismatch")
	}
	if !slices.Equal(f.GatedScenarios, env.GatedScenarios(config)) || !slices.Equal(f.ReportedScenarios, env.ReportedScenarios()) {
		return f, fmt.Errorf("scenario list mismatch")
	}
	contexts, err := scenarioContexts(config)
	if err != nil {
		return f, err
	}
	if !sameContexts(f.ScenarioContexts, contexts) {
		return f, fmt.Errorf("scenario context mismatch")
	}

	for _, v := range scenarios.Variants() {
		b, err := jsonv2.Marshal(v.Policy, jsonv2.Deterministic(true))
		if err != nil {
			return f, err
		}
		var want, actual any
		if err := json.Unmarshal(b, &want); err != nil {
			return f, err
		}
		if err := json.Unmarshal(f.Policies[v.ID], &actual); err != nil {
			return f, err
		}
		wb, err := json.Marshal(want)
		if err != nil {
			return f, err
		}
		ab, err := json.Marshal(actual)
		if err != nil {
			return f, err
		}
		if string(wb) != string(ab) {
			return f, fmt.Errorf("variant policy mismatch: %s", v.ID)
		}
	}
	return f, nil
}

// DecideLook requires the consecutive prior decisions at the operational boundary.
func DecideLook(e gate.Evaluation, previous []gate.Decision) (gate.Decision, error) {
	if e.Look != len(previous)+1 {
		return gate.Decision{}, fmt.Errorf("missing prior look decisions")
	}
	return gate.Decide(e, previous)
}
func freezePath(dir, config string) string {
	name := "freeze_" + config + ".json"
	if config == "offline" {
		name = "freeze_default.json"
	}
	return filepath.Join(dir, "gate", name)
}

func scenarioContexts(config string) (map[string]json.RawMessage, error) {
	contexts := map[string]json.RawMessage{}
	for _, id := range env.ScenarioIDs(config) {
		data, err := json.Marshal(scenarios.ContextFor(id))
		if err != nil {
			return nil, err
		}
		contexts[id] = data
	}
	return contexts, nil
}

func sameContexts(actual, want map[string]json.RawMessage) bool {
	if len(actual) != len(want) {
		return false
	}
	for id, raw := range want {
		var a, b any
		if json.Unmarshal(actual[id], &a) != nil || json.Unmarshal(raw, &b) != nil {
			return false
		}
		if !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}
