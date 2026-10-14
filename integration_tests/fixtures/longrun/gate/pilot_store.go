package suite

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"

	"example.com/longrun"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/google/uuid"
)

// WritePilotEvidence retains the verified complete pilot projection once.
func WritePilotEvidence(dir, configuration string, experiment *eval.Experiment, profile longrun.ConfigurationProfile, sourceSHA string) (PilotEvidence, error) {
	evidence, err := buildPilotEvidence(dir, configuration, experiment, profile, sourceSHA)
	if err != nil {
		return evidence, err
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return evidence, err
	}
	return evidence, writeOnce(pilotPath(dir, configuration, evidence.ExperimentID), append(data, '\n'))
}

// ReadPilotEvidence checks retained roles, observations and the current immutable export.
func ReadPilotEvidence(dir, configuration string, experiment *eval.Experiment, profile longrun.ConfigurationProfile, sourceSHA string) (PilotEvidence, error) {
	var evidence PilotEvidence
	if experiment == nil {
		return evidence, fmt.Errorf("missing pilot experiment")
	}
	if _, err := uuid.Parse(experiment.Header.ID); err != nil {
		return evidence, err
	}
	if err := validateConfigurationName(configuration); err != nil {
		return evidence, err
	}
	data, err := readFile(dir, filepath.Join("gate", filepath.Base(pilotPath(dir, configuration, experiment.Header.ID))))
	if err != nil {
		return evidence, err
	}
	if err := decodeArtifact(data, &evidence); err != nil {
		return evidence, err
	}
	current, err := buildPilotEvidence(dir, configuration, experiment, profile, sourceSHA)
	if err != nil {
		return evidence, err
	}
	if !reflect.DeepEqual(evidence, current) {
		return evidence, fmt.Errorf("pilot export or recorded identity changed")
	}
	return evidence, nil
}

func buildPilotEvidence(dir, configuration string, experiment *eval.Experiment, profile longrun.ConfigurationProfile, sourceSHA string) (PilotEvidence, error) {
	var out PilotEvidence
	if experiment == nil || experiment.TruncatedTail {
		return out, fmt.Errorf("pilot is missing or truncated")
	}
	h := experiment.Header
	if h.Label != "pilot/"+configuration || h.Status != "complete" || h.Parent != "" || h.RunError != "" || h.Stop != "" || h.Config.Repeats != 2 || len(h.Variants) != 1 || h.Variants[0].ID != "baseline" || h.Planned != len(experiment.Attempts) {
		return out, fmt.Errorf("pilot header is not a complete baseline-only experiment")
	}
	items, err := pilotItems(dir, configuration)
	if err != nil {
		return out, err
	}
	rows := make([]eval.StoredAttempt, len(experiment.Attempts))
	for i, attempt := range experiment.Attempts {
		if attempt == nil {
			return out, fmt.Errorf("pilot contains a missing attempt")
		}
		rows[i] = *attempt
	}
	table, err := gate.FromStored(rows)
	if err != nil {
		return out, err
	}
	out = PilotEvidence{Version: 1, Configuration: configuration, ExperimentID: h.ID, Profile: profile, SourceSHA256: sourceSHA}
	for i, row := range table {
		if row.Infrastructure || len(rows[i].Runs) == 0 {
			return out, fmt.Errorf("pilot has infrastructure failures or absent run evidence")
		}
		item, ok := items[row.Scenario+"/"+row.Item]
		if !ok {
			return out, fmt.Errorf("pilot contains an item outside its partition")
		}
		if err := VerifyMeasures(rows[i], item); err != nil {
			return out, err
		}
		models, err := pilotModels(rows[i], profile)
		if err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, PilotObservation{Key: rows[i].AttemptKey, Success: row.Success, Unsupported: row.Unsupported, Evidence: row.Evidence, Models: models})
	}
	data, err := json.Marshal(struct {
		Header   eval.ExperimentHeader
		Attempts []*eval.StoredAttempt
	}{h, experiment.Attempts})
	if err != nil {
		return out, err
	}
	out.ExperimentSHA256 = gate.Digest(data)
	_, err = VerifyPilot(dir, configuration, out, profile, sourceSHA)
	return out, err
}

func pilotModels(attempt eval.StoredAttempt, profile longrun.ConfigurationProfile) (map[string]string, error) {
	observed := map[string]string{}
	for _, stored := range attempt.Runs {
		record, err := stored.Record()
		if err != nil {
			return nil, err
		}
		for _, run := range record.Runs {
			for _, call := range run.Calls {
				role, ok := profile.Roles[call.Started.ModelID]
				if !ok || call.Completion == nil || call.Completion.Facts == nil {
					return nil, fmt.Errorf("pilot lacks complete observed role identity")
				}
				facts := call.Completion.Facts
				if facts.ProviderConfig != role.Provider || model.NormalizeProviderModel(role.Provider, facts.Model) != role.Normalized {
					return nil, fmt.Errorf("pilot observed model differs from declared role %s", call.Started.ModelID)
				}
				observed[call.Started.ModelID] = role.Normalized
			}
		}
	}
	return observed, nil
}

func pilotPath(dir, configuration, id string) string {
	return filepath.Join(dir, "gate", "pilot_"+configuration+"_"+id+".json")
}
