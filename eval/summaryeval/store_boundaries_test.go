package summaryeval

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const replayOne = "01900000-0000-7000-8000-000000000001"

const replayTwo = "01900000-0000-7000-8000-000000000002"

const replayThree = "01900000-0000-7000-8000-000000000003"

func TestReplayResolveSelectsTerminalAndUniquePrefixes(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	writeReplayFixture(t, root, Header{ID: replayOne, Status: "complete", Started: now}, "")
	writeReplayFixture(t, root, Header{ID: replayTwo, Status: "stopped", Started: now}, "")
	writeReplayFixture(t, root, Header{ID: replayThree, Status: "running", Started: now.Add(time.Minute)}, "")
	headers, err := List(root)
	require.NoError(t, err)
	require.Len(t, headers, 3)
	assert.Equal(t, []string{replayThree, replayTwo, replayOne}, []string{headers[0].ID, headers[1].ID, headers[2].ID})
	for _, tt := range []struct{ ref, want string }{{"last", replayTwo}, {replayOne, replayOne}, {replayThree, replayThree}} {
		id, err := Resolve(root, tt.ref)
		require.NoError(t, err)
		assert.Equal(t, tt.want, id)
	}
	for _, ref := range []string{"01900000", "short", "ffffffff"} {
		_, err := Resolve(root, ref)
		require.ErrorIs(t, err, ErrUsage)
	}
	_, err = Resolve(t.TempDir(), "last")
	require.ErrorIs(t, err, ErrUsage)
}

func TestReplayListRetainsValidHeadersAlongsideCorruption(t *testing.T) {
	root := t.TempDir()
	writeReplayFixture(t, root, Header{ID: replayOne, Status: "complete"}, "")
	writeReplayFixture(t, root, Header{ID: replayTwo, Status: "complete"}, "")
	require.NoError(t, os.WriteFile(filepath.Join(root, "_compaction", replayTwo, "replay.json"), []byte("{"), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "_compaction", "_cache"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "_compaction", replayThree), []byte("ignored non-directory"), 0600))
	headers, err := List(root)
	require.Error(t, err)
	require.Len(t, headers, 1)
	assert.Equal(t, replayOne, headers[0].ID)
	_, err = Resolve(root, replayOne)
	require.Error(t, err)
}

func TestReplayLoadRejectsInvalidFramingAndRecords(t *testing.T) {
	validHeader := `{"format":"loom-mcp.eval.compaction.cuts","version":1,"replay":"` + replayOne + `"}` + "\n"
	tests := []struct{ name, cuts string }{
		{"empty", ""},
		{"unterminated header", validHeader[:len(validHeader)-1]},
		{"invalid header json", "{\n"},
		{"wrong format", `{"format":"other","version":1,"replay":"` + replayOne + `"}` + "\n"},
		{"wrong version", `{"format":"loom-mcp.eval.compaction.cuts","version":2,"replay":"` + replayOne + `"}` + "\n"},
		{"wrong replay", `{"format":"loom-mcp.eval.compaction.cuts","version":1,"replay":"` + replayTwo + `"}` + "\n"},
		{"invalid record json", validHeader + "{\n"},
		{"missing key", validHeader + `{"cut":{"run_id":"r"},"repeat":1}` + "\n"},
		{"missing run", validHeader + `{"key":"k","repeat":1}` + "\n"},
		{"invalid repeat", validHeader + `{"key":"k","cut":{"run_id":"r"},"repeat":0}` + "\n"},
		{"unknown field", validHeader + `{"key":"k","cut":{"run_id":"r"},"repeat":1,"future":true}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeReplayFixture(t, root, Header{ID: replayOne, Status: "complete"}, tt.cuts)
			result, err := Load(root, replayOne)
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
	for _, id := range []string{"../escape", "00000000-0000-4000-8000-000000000000", replayTwo} {
		_, err := Load(t.TempDir(), id)
		require.Error(t, err)
	}
}

func TestReplayFilesystemFailuresRemainErrors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "_compaction"), []byte("file"), 0600))
	_, err := List(root)
	require.Error(t, err)
	root = t.TempDir()
	writeReplayFixture(t, root, Header{ID: replayOne, Status: "complete"}, "")
	require.NoError(t, os.Remove(filepath.Join(root, "_compaction", replayOne, "cuts.jsonl")))
	_, err = Load(root, replayOne)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// writeReplayFixture creates persisted public wire data without running a model.
func writeReplayFixture(t *testing.T, root string, header Header, cuts string) {
	t.Helper()
	header.Format = "loom-mcp.eval.compaction"
	header.Version = 1
	data, err := json.Marshal(header)
	require.NoError(t, err)
	dir := filepath.Join(root, "_compaction", header.ID)
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "replay.json"), data, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cuts.jsonl"), []byte(cuts), 0600))
}
