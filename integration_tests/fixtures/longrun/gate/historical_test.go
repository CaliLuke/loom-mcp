package suite_test

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"example.com/longrun/env"
	suite "example.com/longrun/gate"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"time"
)

// historicalFreeze constructs v1 only to prove rejection and archive locks.
func historicalFreeze(dir, config string, calibration []byte, source eval.VCS) (gate.Freeze, error) {
	_, pools, err := suite.Inputs(dir)
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
	contexts, err := historicalScenarioContexts(config)
	if err != nil {
		return gate.Freeze{}, err
	}
	return gate.Freeze{ScenarioContexts: contexts, Dirty: source.Modified, Format: gate.FreezeFormat, Version: 1, Configuration: config, Margins: gate.DefaultMargins(), Alpha: .05, Looks: []int{12, 24, 48}, Repeats: 2, Bootstrap: gate.Bootstrap{Resamples: 10000, Seed: suite.BootstrapSeed}, Guard: -.10, GatedScenarios: env.GatedScenarios(config), ReportedScenarios: env.ReportedScenarios(), Policies: policies, Pools: pools, MaterializerVersion: env.MaterializerVersion, CalibrationSHA256: gate.Digest(calibration), SimulatorVersion: gate.SimulatorVersion, GitRevision: source.Revision, Created: time.Now().UTC()}, nil
}

func historicalScenarioContexts(config string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for _, id := range env.ScenarioIDs(config) {
		data, err := json.Marshal(scenarios.ContextFor(id))
		if err != nil {
			return nil, err
		}
		out[id] = data
	}
	return out, nil
}
