package suite_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommittedFreezeAndValidation(t *testing.T) {
	_, err := suite.VerifyFreeze("..", "offline")
	require.NoError(t, err)
	v, err := suite.ReadValidation("..")
	require.NoError(t, err)
	assert.Equal(t, 3852, v.Attempts)
	assert.Empty(t, v.Failures)
}

func TestFreezeBindsScenarioContract(t *testing.T) {
	for _, kind := range []string{"gated", "reported", "allowance", "dataset_hash"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, suite.WriteItems(dir, 19))
			f, err := suite.BuildFreeze(dir, "offline", []byte("calibration"), eval.VCS{Revision: "test"})
			require.NoError(t, err)
			switch kind {
			case "gated":
				f.GatedScenarios = f.GatedScenarios[1:]
			case "reported":
				f.ReportedScenarios = nil
			case "dataset_hash":
				f.Pools[0].DatasetHash = "flipped"
			case "allowance":
				b, err := json.Marshal(f)
				require.NoError(t, err)
				var raw map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(b, &raw))
				require.NotEmpty(t, raw["scenario_contexts"], "freeze must bind drive settings")
				var contexts map[string]map[string]int
				require.NoError(t, json.Unmarshal(raw["scenario_contexts"], &contexts))
				contexts["recall_pressure"]["RecallReadCalls"]++
				raw["scenario_contexts"], err = json.Marshal(contexts)
				require.NoError(t, err)
				b, err = json.Marshal(raw)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(b, &f))
			}
			require.NoError(t, suite.WriteFreeze(dir, f))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "gate/calibration_default.json"), []byte("calibration"), 0600))
			_, err = suite.VerifyFreeze(dir, "offline")
			assert.Error(t, err, "trusted hash cannot authorize changed scenario contract")
		})
	}
}

func TestCommittedDirtyProvenance(t *testing.T) {
	b, err := os.ReadFile("freeze_default.json")
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &raw))
	var dirty bool
	require.NoError(t, json.Unmarshal(raw["dirty"], &dirty))
	assert.True(t, dirty, "uncommitted fixture must identify dirty provenance")
}

func TestCommittedArtifactsRejectCorruption(t *testing.T) {
	source, err := os.OpenRoot(".")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, source.Close()) })
	for _, kind := range []string{"dataset_hash", "pool_row"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "testdata"), 0750))
			require.NoError(t, os.CopyFS(filepath.Join(dir, "testdata/pools"), os.DirFS("../testdata/pools")))
			require.NoError(t, os.Mkdir(filepath.Join(dir, "gate"), 0750))
			root, err := os.OpenRoot(dir)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, root.Close()) })
			for _, name := range []string{"freeze_default.json", "freeze_default.sha256", "calibration_default.json", "validation.json"} {
				data, err := source.ReadFile(name)
				require.NoError(t, err)
				require.NoError(t, root.WriteFile("gate/"+name, data, 0600))
			}
			if kind == "dataset_hash" {
				data, err := root.ReadFile("gate/freeze_default.json")
				require.NoError(t, err)
				var f gate.Freeze
				require.NoError(t, json.Unmarshal(data, &f))
				f.Pools[0].DatasetHash = "flipped"
				data, err = json.Marshal(f)
				require.NoError(t, err)
				require.NoError(t, root.WriteFile("gate/freeze_default.json", data, 0600))
				hash, err := f.Hash()
				require.NoError(t, err)
				require.NoError(t, root.WriteFile("gate/freeze_default.sha256", []byte(hash+"\n"), 0600))
			} else {
				path := "testdata/pools/fact_early/look1.jsonl"
				data, err := root.ReadFile(path)
				require.NoError(t, err)
				edited := bytes.Replace(data, []byte("Audit"), []byte("Alter"), 1)
				require.NotEqual(t, data, edited)
				require.NoError(t, root.WriteFile(path, edited, 0600))
				_, err = suite.ReadValidation(dir)
				require.Error(t, err)
			}
			_, err = suite.VerifyFreeze(dir, "offline")
			require.Error(t, err)
		})
	}
}
