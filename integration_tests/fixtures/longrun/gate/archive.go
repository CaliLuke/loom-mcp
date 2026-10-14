package suite

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// V1Archive binds exact historical artifacts to their original and retained
// source identities and the explicitly declared migration inventory.
type V1Archive struct {
	Version          int               `json:"version"`
	Configuration    string            `json:"configuration"`
	FreezeSHA256     string            `json:"freeze_sha256"`
	InventorySHA256  string            `json:"inventory_sha256"`
	OriginalRevision string            `json:"original_revision"`
	OriginalDirty    bool              `json:"original_dirty"`
	RetainedRevision string            `json:"retained_revision"`
	Files            map[string]string `json:"files"`
	Explanation      string            `json:"explanation"`
}

// WriteInventory writes the explicit store declaration and content inventory once.
func WriteInventory(dir string, in StoreInventory) error {
	checked, err := finishInventory(in)
	if err != nil {
		return err
	}
	if checked.SHA256 != in.SHA256 || !in.DeclaredComplete || in.Configuration == "" || !filepath.IsAbs(in.Store) {
		return fmt.Errorf("invalid inventory declaration or digest")
	}
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	return writeOnce(filepath.Join(dir, "gate", "inventory_"+in.Configuration+".json"), append(data, '\n'))
}

// ArchiveV1 preserves exact v1 bytes before removing their active artifact names.
// Unknown or consumed live evidence cannot authorize migration.
func ArchiveV1(dir, configuration, inventoryPath, retainedRevision string) (V1Archive, error) {
	var out V1Archive
	inventory, err := readMigrationInventory(dir, configuration, inventoryPath)
	if err != nil {
		return out, err
	}
	revisionBytes, err := hex.DecodeString(retainedRevision)
	if err != nil || len(revisionBytes) != 20 {
		return out, fmt.Errorf("archive requires a retained source commit")
	}
	freezeFile := filepath.Base(freezePath(dir, configuration))
	freezeBytes, err := readFile(dir, "gate/"+freezeFile)
	if err != nil {
		return out, err
	}
	var f gate.Freeze
	if err := json.Unmarshal(freezeBytes, &f); err != nil {
		return out, err
	}
	if f.Format != gate.FreezeFormat || f.Version != 1 || f.Configuration != configuration || f.GitRevision == "" {
		return out, fmt.Errorf("archive requires the original v1 configuration and source identity")
	}
	hash, err := f.Hash()
	if err != nil {
		return out, err
	}
	sidecarName := strings.TrimSuffix(freezeFile, ".json") + ".sha256"
	sidecar, err := readFile(dir, "gate/"+sidecarName)
	if err != nil || strings.TrimSpace(string(sidecar)) != hash {
		return out, fmt.Errorf("historical freeze hash mismatch")
	}
	calibrationFile := filepath.Base(CalibrationPath(dir, configuration))
	calibration, err := readFile(dir, "gate/"+calibrationFile)
	if err != nil {
		return out, err
	}
	if gate.Digest(calibration) != f.CalibrationSHA256 {
		return out, fmt.Errorf("historical calibration hash mismatch")
	}
	textFile := strings.TrimSuffix(calibrationFile, ".json") + ".txt"
	textBytes, err := readFile(dir, "gate/"+textFile)
	if err != nil {
		return out, err
	}
	files := map[string][]byte{freezeFile: freezeBytes, sidecarName: sidecar, calibrationFile: calibration, textFile: textBytes}
	if configuration == "offline" {
		validation, readErr := readFile(dir, "gate/validation.json")
		if readErr == nil {
			files["validation.json"] = validation
		} else if !os.IsNotExist(readErr) {
			return out, readErr
		}
	}
	out = V1Archive{Version: 1, Configuration: configuration, FreezeSHA256: hash, InventorySHA256: inventory.SHA256, OriginalRevision: f.GitRevision, OriginalDirty: f.Dirty, RetainedRevision: retainedRevision, Files: map[string]string{}, Explanation: "Historical v1 evidence is not a v2 qualification. Reproduce its verification using the retained source commit; archived pools remain locked."}
	archiveDir := filepath.Join(dir, "gate/archive/v1", configuration, hash)
	if err := os.MkdirAll(filepath.Dir(archiveDir), 0750); err != nil {
		return out, err
	}
	if err := os.Mkdir(archiveDir, 0750); err != nil {
		return out, err
	}
	for name, data := range files {
		if err := writeOnce(filepath.Join(archiveDir, name), data); err != nil {
			return out, err
		}
		out.Files[name] = gate.Digest(data)
	}
	metadata, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return out, err
	}
	if err := writeOnce(filepath.Join(archiveDir, "archive.json"), append(metadata, '\n')); err != nil {
		return out, err
	}
	// Verify every retained byte before removing any active artifact name.
	for name, want := range files {
		copied, err := readFile(archiveDir, name)
		if err != nil || !bytes.Equal(copied, want) {
			return out, fmt.Errorf("archive copy verification failed: %s", name)
		}
		original, err := readFile(dir, "gate/"+name)
		if err != nil || !bytes.Equal(original, want) {
			return out, fmt.Errorf("historical artifact changed during archive: %s", name)
		}
	}
	for name := range files {
		if err := os.Remove(filepath.Join(dir, "gate", name)); err != nil {
			return out, err
		}
	}
	return out, nil
}

func readMigrationInventory(dir, configuration, path string) (StoreInventory, error) {
	var in StoreInventory
	data, err := readFile(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return in, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return in, err
	}
	checked, err := finishInventory(in)
	if err != nil {
		return in, err
	}
	if in.Version != 1 || in.Configuration != configuration || !in.DeclaredComplete || in.Unknown || in.Consumed || checked.SHA256 != in.SHA256 {
		return in, fmt.Errorf("migration inventory is unknown, consumed or invalid")
	}
	current, err := InventoryStore(dir, configuration, in.Store, true)
	if err != nil {
		return in, err
	}
	if current.SHA256 != in.SHA256 {
		return in, fmt.Errorf("declared inventory changed; migration refused")
	}
	return in, nil
}
