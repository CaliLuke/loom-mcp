package suite_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func historicalArchiveFixture(t *testing.T) (string, string, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "gate"), 0700))
	calibration := []byte("historical calibration\n")
	f := gate.Freeze{Format: gate.FreezeFormat, Version: 1, Configuration: "offline", CalibrationSHA256: gate.Digest(calibration), GitRevision: "8d30b51d118fa7b408827ba03574865043eaee2c", Dirty: true}
	data, err := json.MarshalIndent(f, "", "  ")
	require.NoError(t, err)
	hash, err := f.Hash()
	require.NoError(t, err)
	files := map[string][]byte{"freeze_default.json": append(data, '\n'), "freeze_default.sha256": []byte(hash + "\n"), "calibration_default.json": calibration, "calibration_default.txt": []byte("historical report\n")}
	for name, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gate", name), data, 0600))
	}
	return dir, hash, files
}

func TestK19cContractArchivePreservesBytesAndPoolLock(t *testing.T) {
	dir, hash, files := historicalArchiveFixture(t)
	inv, err := suite.InventoryStore(dir, "offline", filepath.Join(dir, "declared-store"), true)
	require.NoError(t, err)
	require.NoError(t, suite.WriteInventory(dir, inv))
	require.Error(t, suite.WriteInventory(dir, inv), "declarations are write once")
	path := filepath.Join(dir, "gate/inventory_offline.json")
	archive, err := suite.ArchiveV1(dir, "offline", path, "1d5373721058f8df787bc795dc37a98b315a2178")
	require.NoError(t, err)
	assert.Equal(t, hash, archive.FreezeSHA256)
	assert.Equal(t, inv.SHA256, archive.InventorySHA256)
	for name, want := range files {
		confined, err := os.OpenRoot(dir)
		require.NoError(t, err)
		got, err := confined.ReadFile(filepath.Join("gate/archive/v1/offline", hash, name))
		require.NoError(t, confined.Close())
		require.NoError(t, err)
		assert.Equal(t, want, got)
		_, err = os.Stat(filepath.Join(dir, "gate", name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.Error(t, suite.WriteItems(dir, 99), "archived freeze remains an immutable pool lock")
	_, err = suite.ArchiveV1(dir, "offline", path, "1d5373721058f8df787bc795dc37a98b315a2178")
	require.Error(t, err, "no force overwrite")
}

func TestK19cContractArchiveRejectsUnknownOrChangedInventory(t *testing.T) {
	for _, changed := range []bool{false, true} {
		dir, _, _ := historicalArchiveFixture(t)
		store := filepath.Join(dir, "store")
		require.NoError(t, os.Mkdir(store, 0700))
		if !changed {
			require.NoError(t, os.WriteFile(filepath.Join(store, "request.json"), []byte("unknown"), 0600))
		}
		inv, err := suite.InventoryStore(dir, "offline", store, true)
		require.NoError(t, err)
		require.NoError(t, suite.WriteInventory(dir, inv))
		if changed {
			require.NoError(t, os.WriteFile(filepath.Join(store, "later-request.json"), []byte("changed after declaration"), 0600))
		}
		_, err = suite.ArchiveV1(dir, "offline", filepath.Join(dir, "gate/inventory_offline.json"), "1d5373721058f8df787bc795dc37a98b315a2178")
		require.Error(t, err)
		_, err = os.Stat(filepath.Join(dir, "gate/freeze_default.json"))
		require.NoError(t, err, "failure cannot remove historical evidence")
	}
}
