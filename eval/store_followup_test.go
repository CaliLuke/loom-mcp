package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreListRetainsValidHeaders(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	require.NoError(t, w.Finish(storeReport(t, cfg), nil))
	malformed := "0199d770-0000-7000-8000-000000000001"
	missing := "0199d770-0000-7000-8000-000000000002"
	require.NoError(t, os.Mkdir(filepath.Join(s.dir, malformed), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, malformed, "experiment.json"), []byte(`{broken}`), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(s.dir, missing), 0700))
	headers, err := s.List()
	require.Error(t, err)
	assert.Contains(t, err.Error(), malformed)
	assert.Contains(t, err.Error(), missing)
	require.Len(t, headers, 1)
	assert.Equal(t, w.ID(), headers[0].ID)
	_, err = s.Load(malformed)
	require.Error(t, err)
}

func TestStoreRunBearingExactReencoding(t *testing.T) {
	// The legacy fixture is read-only and contains multi-run sessions and a child.
	e := loadReportFixture(t)
	data, err := os.ReadFile(filepath.Join(e.Dir, "attempts.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	require.Len(t, lines, len(e.Attempts)+1)
	for i, a := range e.Attempts {
		require.NotEmpty(t, a.Runs)
		encoded, err := encodeStore(a)
		require.NoError(t, err)
		assert.Equal(t, lines[i+1], string(encoded))
		decoded, err := decodeAttemptLine(encoded)
		require.NoError(t, err)
		again, err := encodeStore(decoded)
		require.NoError(t, err)
		assert.Equal(t, encoded, again)
	}
}

func TestStoreCurrentRunsExactReencoding(t *testing.T) {
	cfg := Config{
		Harness: NewHarnessFunc(registerReportTools, nil, agenttest.PassthroughAll(), agenttest.ScriptedModel("report",
			agenttest.Respond(agenttest.RawToolCall("delegate.child", rawjson.Message(`{}`))),
			agenttest.Respond(agenttest.Text("child done")),
			agenttest.Respond(agenttest.Text("first done")),
			agenttest.Respond(agenttest.Text("second done")),
		)),
		Scenarios: []Scenario{{ID: "chat", Agent: "report", Items: Items(Item{ID: "multi", Turns: []string{"first", "second"}}), Expect: []expect.Expectation{expect.Completed()}}},
	}
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	cfg.OnAttempt = w.OnAttempt(nil)
	report, err := Run(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, w.Finish(report, nil))
	loaded, err := s.Load(w.ID())
	require.NoError(t, err)
	require.Len(t, loaded.Attempts, 1)
	a := loaded.Attempts[0]
	assert.Equal(t, VerdictPassed, a.Verdict)
	require.Len(t, a.Runs, 2)
	rec, err := a.Runs[0].Record()
	require.NoError(t, err)
	assert.Len(t, rec.Runs, 2, "parent and child are both recorded")
	data, err := os.ReadFile(filepath.Join(loaded.Dir, "attempts.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	require.Len(t, lines, 2)
	encoded, err := encodeStore(a)
	require.NoError(t, err)
	assert.Equal(t, lines[1], string(encoded))
	decoded, err := decodeAttemptLine(encoded)
	require.NoError(t, err)
	again, err := encodeStore(decoded)
	require.NoError(t, err)
	assert.Equal(t, encoded, again)
}
