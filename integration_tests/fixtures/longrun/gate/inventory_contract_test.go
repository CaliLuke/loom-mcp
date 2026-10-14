package suite_test

import (
	"strings"

	"os"
	"path/filepath"
	"testing"

	suite "example.com/longrun/gate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cContractInventoryDeclarationAndMissingStore(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "authoritative-store")
	_, err := suite.InventoryStore(dir, "offline", store, false)
	require.Error(t, err)
	got, err := suite.InventoryStore(dir, "offline", store, true)
	require.NoError(t, err)
	assert.True(t, got.Absent)
	assert.True(t, got.DeclaredComplete)
	assert.Equal(t, store, got.Store)
	assert.NotEmpty(t, got.SHA256)
	assert.Contains(t, got.Limitation, "external")
	_, err = os.Stat(store)
	require.ErrorIs(t, err, os.ErrNotExist, "inventory must not create an empty store as evidence")
}

func TestK19cContractInventorySeesUnpublishedAndMalformedEvidence(t *testing.T) {
	for _, tc := range []struct{ name, path, data, kind string }{
		{"hidden staging", ".experiment.tmp-123/attempts.jsonl", `{"variant":"hybrid","item":"heldout"}`, "staging"},
		{"temporary header", ".experiment.json.tmp-123", `{"status":"running"}`, "temporary_header"},
		{"orphan UUID", "0199d77a-0000-7000-8000-000000000001/attempts.jsonl", `{"format":"loom-mcp.eval.attempts"}`, "orphan"},
		{"invalid nested header", "0199d77a-0000-7000-8000-000000000001/experiment.json", `{"format":"loom-mcp.eval.experiment","version":1,"id":"0199d77a-0000-7000-8000-000000000001","status":"complete","planned":1,"config":{"repeats":"invalid"}}`, "invalid_header"},
		{"invalid header", "0199d77a-0000-7000-8000-000000000001/experiment.json", `{`, "invalid_header"},
		{"running header", "0199d77a-0000-7000-8000-000000000001/experiment.json", `{"format":"loom-mcp.eval.experiment","version":1,"id":"0199d77a-0000-7000-8000-000000000001","status":"running","label":"gate/anthropic/abc/look1"}`, "running"},
		{"aborted header", "0199d77a-0000-7000-8000-000000000001/experiment.json", `{"format":"loom-mcp.eval.experiment","version":1,"id":"0199d77a-0000-7000-8000-000000000001","status":"stopped","label":"gate/anthropic/abc/look1"}`, "partial"},
		{"unknown entry", "requests.jsonl", `{"variant":"hybrid","item":"heldout"}`, "unrecognized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := filepath.Join(dir, "store")
			path := filepath.Join(store, tc.path)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			require.NoError(t, os.WriteFile(path, []byte(tc.data), 0600))
			got, err := suite.InventoryStore(dir, "anthropic", store, true)
			require.NoError(t, err)
			assert.True(t, got.Unknown, "uncertain request consumption is never unused evidence")
			assert.NotEmpty(t, got.Files, "hidden and malformed bytes still belong to inventory")
			assert.Contains(t, inventoryKinds(got), tc.kind)
		})
	}
}

func TestK19cContractInventoryDoesNotFollowExternalSymlinks(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	require.NoError(t, os.Mkdir(store, 0700))
	external := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(external, "secret"), []byte("external bytes"), 0600))
	require.NoError(t, os.Symlink(external, filepath.Join(store, "linked")))
	got, err := suite.InventoryStore(dir, "offline", store, true)
	require.NoError(t, err)
	assert.True(t, got.Unknown)
	assert.Empty(t, got.Files)
	assert.Contains(t, inventoryKinds(got), "symlink")
}

func inventoryKinds(in suite.StoreInventory) []string {
	kinds := make([]string, 0, len(in.Entries))
	for _, entry := range in.Entries {
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

func TestK19cContractInventoryCompleteAndConsumedAttempts(t *testing.T) {
	for _, tc := range []struct {
		name, variant, tail string
		consumed, unknown   bool
	}{
		{"baseline only", "baseline", "", false, false},
		{"heldout variant", "hybrid", "", true, false},
		{"torn request tail", "baseline", `{"variant":"hybrid"`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := filepath.Join(dir, "store")
			id := "0199d77a-0000-7000-8000-000000000001"
			require.NoError(t, os.MkdirAll(filepath.Join(store, id), 0700))
			require.NoError(t, suite.WriteItems(dir, 19))
			pool, err := suite.LoadPool(dir, "fact_early", "look1")
			require.NoError(t, err)
			itemID := pool.Items()[0].ID
			header := `{"format":"loom-mcp.eval.experiment","version":1,"id":"` + id + `","status":"complete","planned":1,"label":"gate/anthropic/abc/look1"}`
			attempts := `{"format":"loom-mcp.eval.attempts","version":1,"experiment":"` + id + `"}` + "\n" + `{"variant":"` + tc.variant + `","scenario":"s","item":"heldout","repeat":2,"key":"` + tc.variant + `:s:heldout:2","verdict":"failed"}` + "\n" + tc.tail
			attempts = strings.ReplaceAll(attempts, "heldout", itemID)
			attempts = strings.ReplaceAll(attempts, `"scenario":"s"`, `"scenario":"fact_early"`)
			attempts = strings.ReplaceAll(attempts, ":s:", ":fact_early:")
			require.NoError(t, os.WriteFile(filepath.Join(store, id, "experiment.json"), []byte(header), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(store, id, "attempts.jsonl"), []byte(attempts), 0600))
			got, err := suite.InventoryStore(dir, "anthropic", store, true)
			require.NoError(t, err)
			assert.Equal(t, tc.consumed, got.Consumed, "persisted outcomes consume evidence even when quality failed")
			assert.Equal(t, tc.unknown, got.Unknown)
		})
	}
}

func TestK19cContractInventoryTreatsRequestWithoutOutcomeAsUnknown(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	id := "0199d77a-0000-7000-8000-000000000001"
	require.NoError(t, os.MkdirAll(filepath.Join(store, id), 0700))
	require.NoError(t, suite.WriteItems(dir, 19))
	pool, err := suite.LoadPool(dir, "fact_early", "look1")
	require.NoError(t, err)
	itemID := pool.Items()[0].ID
	header := `{"format":"loom-mcp.eval.experiment","version":1,"id":"` + id + `","status":"complete","planned":1,"label":"gate/anthropic/abc/look1"}`
	attempts := `{"format":"loom-mcp.eval.attempts","version":1,"experiment":"` + id + `"}` + "\n" + `{"variant":"baseline","scenario":"s","item":"heldout","repeat":2}` + "\n"
	attempts = strings.ReplaceAll(attempts, "heldout", itemID)
	attempts = strings.ReplaceAll(attempts, `"scenario":"s"`, `"scenario":"fact_early"`)
	attempts = strings.ReplaceAll(attempts, ":s:", ":fact_early:")
	require.NoError(t, os.WriteFile(filepath.Join(store, id, "experiment.json"), []byte(header), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(store, id, "attempts.jsonl"), []byte(attempts), 0600))
	got, err := suite.InventoryStore(dir, "anthropic", store, true)
	require.NoError(t, err)
	assert.True(t, got.Unknown, "a keyed request does not establish a completed observation")
}

func TestK19cContractInventoryRejectsMissingRequiredHeldoutPool(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, suite.WriteItems(dir, 19))
	store := filepath.Join(dir, "store")
	id := "0199d77a-0000-7000-8000-000000000001"
	require.NoError(t, os.MkdirAll(filepath.Join(store, id), 0700))
	header := `{"format":"loom-mcp.eval.experiment","version":1,"id":"` + id + `","status":"complete","planned":1,"label":"gate/anthropic/abc/look1"}`
	attempts := `{"format":"loom-mcp.eval.attempts","version":1,"experiment":"` + id + `"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(store, id, "experiment.json"), []byte(header), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(store, id, "attempts.jsonl"), []byte(attempts), 0600))
	require.NoError(t, os.Remove(filepath.Join(dir, "testdata/pools/recall_pressure/look2.jsonl")))
	_, err := suite.InventoryStore(dir, "anthropic", store, true)
	require.Error(t, err, "a partial nonempty heldout manifest cannot establish unused evidence")
}
