package suite

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cCommittedArtifacts(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	evidence, err := readFreezeEvidence(t.Context(), "..", "offline")
	require.NoError(t, err)
	assert.Equal(t, 2940, evidence.validation.Attempts)
	assert.Empty(t, evidence.validation.Failures)
	assert.Equal(t, env.MaterializerVersion, evidence.validation.MaterializerVersion)
	assert.True(t, evidence.source.Dirty, "retained source bundle identifies the uncommitted implementation")
	require.Len(t, evidence.calibration.Candidates, 3)
	for _, candidate := range evidence.calibration.Candidates {
		assert.Len(t, candidate.Report.Cells, 1620)
		assert.Len(t, candidate.Report.Sensitivity, 2)
	}
	data, err := os.ReadFile("calibration_default.txt")
	require.NoError(t, err)
	assert.Equal(t, CalibrationText("offline", evidence.calibration), string(data))
	eligible := false
	for _, selection := range evidence.calibration.Selections {
		eligible = eligible || selection.Status == gate.CalibrationEligible
	}
	if eligible {
		f, err := VerifyFreeze("..", "offline")
		require.NoError(t, err)
		assert.Equal(t, evidence.calibration.Selections, f.Selections)
	} else {
		_, err := os.Stat("freeze_default.json")
		require.ErrorIs(t, err, os.ErrNotExist)
		_, err = os.Stat("freeze_default.sha256")
		require.ErrorIs(t, err, os.ErrNotExist)
		_, err = buildEvidenceFreeze("offline", evidence)
		require.ErrorContains(t, err, "no calibration-eligible variant")
	}
}

func TestK19cArchiveIntegrity(t *testing.T) {
	paths, err := filepath.Glob("archive/v1/offline/*/archive.json")
	require.NoError(t, err)
	require.Len(t, paths, 1)
	root, err := os.OpenRoot(filepath.Dir(paths[0]))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	data, err := root.ReadFile("archive.json")
	require.NoError(t, err)
	var archive V1Archive
	require.NoError(t, decodeArtifact(data, &archive))
	require.Equal(t, 1, archive.Version)
	require.Equal(t, "offline", archive.Configuration)
	require.Len(t, archive.Files, 5)
	for name, digest := range archive.Files {
		data, err := root.ReadFile(name)
		require.NoError(t, err)
		assert.Equal(t, digest, gate.Digest(data), name)
		original, err := sourceGit(t.Context(), "../../../..", "show", archive.RetainedRevision+":integration_tests/fixtures/longrun/gate/"+name)
		require.NoError(t, err)
		assert.Equal(t, original, data, name)
	}
	data, err = root.ReadFile("freeze_default.json")
	require.NoError(t, err)
	var frozen gate.Freeze
	require.NoError(t, json.Unmarshal(data, &frozen))
	hash, err := frozen.Hash()
	require.NoError(t, err)
	assert.Equal(t, archive.FreezeSHA256, hash)
	assert.Equal(t, archive.OriginalRevision, frozen.GitRevision)
	assert.Equal(t, archive.OriginalDirty, frozen.Dirty)
	data, err = os.ReadFile("inventory_offline.json")
	require.NoError(t, err)
	var inventory StoreInventory
	require.NoError(t, decodeArtifact(data, &inventory))
	assert.Equal(t, inventory.SHA256, archive.InventorySHA256)
	temp := t.TempDir()
	require.NoError(t, WriteFreeze(temp, frozen))
	_, err = VerifyFreeze(temp, "offline")
	require.ErrorContains(t, err, "historical freeze v1")
	// Copy only the archive into a temporary directory: its presence alone locks pools.
	require.NoError(t, os.CopyFS(filepath.Join(temp, "gate/archive"), os.DirFS("archive")))
	require.NoError(t, os.Remove(filepath.Join(temp, "gate/freeze_default.json")))
	require.Error(t, WriteItems(temp, 19))
	_, err = os.Stat(filepath.Join(temp, "testdata"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestK19cDiagnosticWithoutFreeze(t *testing.T) {
	_, evidence := frozenEvidenceFixture(t)
	for i := range evidence.calibration.Selections {
		evidence.calibration.Selections[i] = gate.CandidateSelection{Variant: evidence.calibration.Selections[i].Variant, Status: gate.CalibrationIneligible}
	}
	_, err := buildEvidenceFreeze("offline", evidence)
	require.ErrorContains(t, err, "no calibration-eligible variant")
	text := CalibrationText("offline", evidence.calibration)
	assert.Contains(t, text, string(gate.CalibrationIneligible))
	assert.Contains(t, text, "cells: 1620")
}

func TestFreezeBindsScenarioContract(t *testing.T) {
	for _, kind := range []string{"gated", "reported", "allowance", "dataset_hash", "profile", "source", "validation"} {
		t.Run(kind, func(t *testing.T) {
			f, evidence := frozenEvidenceFixture(t)
			hash, err := f.Hash()
			require.NoError(t, err)
			_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
			require.NoError(t, err)
			// Copy through the wire so mutation cannot also change the trusted inputs.
			data, err := json.Marshal(f)
			require.NoError(t, err)
			f = gate.Freeze{}
			require.NoError(t, json.Unmarshal(data, &f))
			switch kind {
			case "gated":
				f.GatedScenarios = f.GatedScenarios[1:]
			case "reported":
				f.ReportedScenarios = nil
			case "dataset_hash":
				f.Pools[0].DatasetHash = "flipped"
			case "allowance":
				var context map[string]int
				require.NoError(t, json.Unmarshal(f.ScenarioContexts["recall_pressure"], &context))
				context["RecallReadCalls"]++
				f.ScenarioContexts["recall_pressure"], err = json.Marshal(context)
				require.NoError(t, err)
			case "profile":
				delete(f.Profiles, "multimodal_offline")
			case "source":
				f.SourceSHA256 = strings.Repeat("f", 64)
			case "validation":
				f.ValidationSHA256 = strings.Repeat("f", 64)
			}
			hash, err = f.Hash()
			require.NoError(t, err)
			_, err = verifyFreezeEvidence(f, hash, evidence, "offline")
			require.Error(t, err, "resealing cannot authorize changed evidence")
		})
	}
}

func TestCommittedPoolCorruptionRefusesValidation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(filepath.Join(dir, "testdata/pools"), os.DirFS("../testdata/pools")))
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	v, err := ReadValidationFor("..", "offline")
	require.NoError(t, err)
	require.NoError(t, VerifyValidation(dir, "offline", v))
	path := "testdata/pools/fact_early/look1.jsonl"
	data, err := root.ReadFile(path)
	require.NoError(t, err)
	edited := bytes.Replace(data, []byte("Audit"), []byte("Alter"), 1)
	require.NotEqual(t, data, edited)
	require.NoError(t, root.WriteFile(path, edited, 0600))
	require.ErrorContains(t, VerifyValidation(dir, "offline", v), "pool identity mismatch")
}
