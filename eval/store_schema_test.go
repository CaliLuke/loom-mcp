package eval

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Acceptance 4: freeze the complete attempt wire shape independently of live IDs.
func TestStoreAttemptGolden(t *testing.T) {
	r := &AttemptRecord{AttemptKey: AttemptKey{Variant: "base", Scenario: "chat", Item: "one", Repeat: 1}, Index: 0, Started: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), Duration: time.Second, Tokens: 3, Outcome: &Outcome{Task: "answer", Reference: "yes", Checks: []expect.Check{{Name: "ok", Passed: true}}, Scores: []expect.Score{{Name: "quality", Value: 1}}, Claims: []Claim{{ID: "helpful", Text: "Helpful", Informational: true}}}}
	a, err := storedAttempt(r)
	require.NoError(t, err)
	data, err := encodeStore(a)
	require.NoError(t, err)
	want, err := readStoreTestFile("testdata/store/attempt.golden.jsonl")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(data)+"\n")
	decoded, err := decodeAttemptLine(data)
	require.NoError(t, err)
	again, err := encodeStore(decoded)
	require.NoError(t, err)
	assert.Equal(t, data, again)
}

// Acceptance 14: accepted lines have a stable canonical re-encoding.
func FuzzDecodeAttemptLine(f *testing.F) {
	golden, err := readStoreTestFile("testdata/store/attempt.golden.jsonl")
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range []string{"attempt.golden.jsonl", "attempt_judged.golden.jsonl"} {
		data, err := readStoreTestFile("testdata/store/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(strings.Replace(string(golden), `"runs":[]`, `"runs":[{"run_id":"root","final":{"Role":"assistant","Parts":[{"Kind":"text","Text":"done"}],"Meta":{"large":9007199254740993}},"export":{"version":1,"root_run_id":"root","runs":[],"content":{}}}]`, 1)))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"index":0,"variant":"base","scenario":"chat","item":"one","repeat":1,"key":"base:chat:one:1","verdict":"passed"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := decodeAttemptLine(data)
		if err != nil {
			return
		}
		first, err := encodeStore(a)
		require.NoError(t, err)
		b, err := decodeAttemptLine(first)
		require.NoError(t, err)
		second, err := encodeStore(b)
		require.NoError(t, err)
		assert.Equal(t, string(first), strings.TrimSpace(string(second)))
	})
}

func TestStoreStrictDocuments(t *testing.T) {
	header := `{"format":"loom-mcp.eval.attempts","version":1,"experiment":"fixed"}` + "\n"
	attempt, err := readStoreTestFile("testdata/store/attempt.golden.jsonl")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, body string
		line       int
	}{
		{"missing", "", 1},
		{"wrong format", strings.Replace(header, "eval.attempts", "wrong", 1), 1},
		{"version", strings.Replace(header, `"version":1`, `"version":2`, 1), 1},
		{"mismatch", strings.Replace(header, "fixed", "other", 1), 1},
		{"unknown", strings.Replace(header, `"version":1`, `"version":1,"extra":0`, 1), 1},
		{"duplicate", strings.Replace(header, `"version":1`, `"version":1,"\u0076ersion":1`, 1), 1},
		{"middle", header + "{broken}\n" + string(attempt), 2},
		{"bad export", header + strings.Replace(string(attempt), `"runs":[]`, `"runs":[{"run_id":"r","export":{"version":2}}]`, 1), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := readAttempts(strings.NewReader(tc.body), "attempts.jsonl", &ExperimentHeader{ID: "fixed", Version: 1})
			var located *DecodeError
			require.ErrorAs(t, err, &located)
			assert.Equal(t, tc.line, located.Line)
			assert.Equal(t, "attempts.jsonl", located.Source)
		})
	}
	records, truncated, err := readAttempts(strings.NewReader(header+string(attempt)+`{"index":`), "attempts.jsonl", &ExperimentHeader{ID: "fixed", Version: 1})
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Len(t, records, 1)
	for _, data := range []string{
		`{"format":"bad","version":1,"id":"fixed","status":"running"}`,
		`{"format":"loom-mcp.eval.experiment","version":2,"id":"fixed","status":"running"}`,
		`{"format":"loom-mcp.eval.experiment","version":1,"id":"fixed","status":"running","extra":0}`,
	} {
		_, err := decodeExperiment([]byte(data), "fixed")
		require.Error(t, err)
	}
}

func TestStoreErrorsAndExactReencoding(t *testing.T) {
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	cfg := storeConfig()
	cfg.Repeats = 2
	w, err := s.Begin(t.Context(), cfg, ExperimentMeta{Identity: &Identity{}})
	require.NoError(t, err)
	report := storeReport(t, cfg)
	report.Attempts[0].HarnessErr = errors.New("setup failed")
	report.Attempts[0].Outcome.Err = errors.New("assertion failed")
	// Completion order differs from the report's declaration order.
	require.NoError(t, w.Append(report.Attempts[1]))
	require.NoError(t, w.Append(report.Attempts[0]))
	require.NoError(t, w.Finish(report, nil))
	loaded, err := s.Load(w.ID())
	require.NoError(t, err)
	assert.Equal(t, "setup failed", loaded.Attempts[0].HarnessError)
	assert.Equal(t, "assertion failed", loaded.Attempts[0].Error)
	assert.Equal(t, VerdictError, loaded.Attempts[0].Verdict)
	data, err := readStoreTestFile(filepath.Join(loaded.Dir, "attempts.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i, a := range loaded.Attempts {
		encoded, err := encodeStore(a)
		require.NoError(t, err)
		assert.Equal(t, lines[2-i], string(encoded))
	}
}

func TestStoreFinalRejectsUnknownMembers(t *testing.T) {
	for _, data := range []string{
		`{"final":{"Role":"assistant","Parts":[],"Meta":{},"unexpected":true}}`,
		`{"final":{"Role":"assistant","Parts":[{"Kind":"text","Text":"done","unexpected":true}],"Meta":{}}}`,
	} {
		var run StoredRun
		require.Error(t, decodeStore([]byte(data), &run))
	}
}
