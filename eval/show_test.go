package eval

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedReportWriter struct{}

func TestShowText(t *testing.T) {
	e := loadReportFixture(t)
	var out bytes.Buffer
	require.NoError(t, Show(&out, e, e.Attempts[0], ShowOptions{}))
	reportGolden(t, "show", out.String())
	session, err := e.Attempts[0].Session()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(out.String(), session.Render(trajectory.RenderOptions{Limits: trajectory.DefaultLimits()})))
}

func TestShowJSON(t *testing.T) {
	e := loadReportFixture(t)
	for _, export := range []bool{false, true} {
		var out, again bytes.Buffer
		require.NoError(t, ShowJSON(&out, e, e.Attempts[0], ShowOptions{Export: export}))
		require.NoError(t, ShowJSON(&again, e, e.Attempts[0], ShowOptions{Export: export}))
		assert.Equal(t, out.Bytes(), again.Bytes())
		var doc struct {
			Format  string
			Version int
			Attempt struct{ Runs []map[string]jsontext.Value }
		}
		require.NoError(t, json.Unmarshal(out.Bytes(), &doc, json.MatchCaseInsensitiveNames(true)))
		assert.Equal(t, "loom-mcp.eval.show", doc.Format)
		assert.Equal(t, 1, doc.Version)
		require.Len(t, doc.Attempt.Runs, 2)
		for i, run := range doc.Attempt.Runs {
			tr, err := e.Attempts[0].Runs[i].Trajectory()
			require.NoError(t, err)
			want, err := tr.Marshal()
			require.NoError(t, err)
			assert.Equal(t, string(want), string(run["trajectory"]))
			_, present := run["export"]
			assert.Equal(t, export, present)
		}
	}
}

func TestShowUnavailable(t *testing.T) {
	for _, runs := range [][]StoredRun{nil, {{RunID: "missing"}}, {{RunID: "bad", Export: jsontext.Value(`{}`)}}} {
		e := &Experiment{}
		a := &StoredAttempt{StorageError: "record too large", Runs: runs}
		var text, data bytes.Buffer
		require.NoError(t, Show(&text, e, a, ShowOptions{}))
		require.NoError(t, ShowJSON(&data, e, a, ShowOptions{}))
		assert.Contains(t, text.String(), "trajectory unavailable:")
		assert.Contains(t, data.String(), `"trajectory_error"`)
	}
}

func TestShowLimitsAndRenderPanic(t *testing.T) {
	e := loadReportFixture(t)
	a := e.Attempts[0]
	a.Runs[0].Final = &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("x", 5000)}}}
	var limited, full bytes.Buffer
	require.NoError(t, Show(&limited, e, a, ShowOptions{}))
	require.NoError(t, Show(&full, e, a, ShowOptions{Full: true}))
	assert.NotContains(t, limited.String(), strings.Repeat("x", 5000))
	assert.Contains(t, full.String(), strings.Repeat("x", 5000))
	text, err := renderReportSession(trajectory.Session{nil}, trajectory.RenderOptions{})
	assert.Empty(t, text)
	require.ErrorContains(t, err, "render")
}

func TestSummaryReasonsAndOrder(t *testing.T) {
	e := loadReportFixture(t)
	original := e.Attempts[0]
	e.Attempts = nil
	for i, tc := range []struct {
		item      string
		verdict   Verdict
		checks    []expect.Check
		claims    []StoredClaim
		judgments []StoredJudgment
		err       string
	}{
		{item: "passed", verdict: VerdictPassed},
		{item: "check", verdict: VerdictFailed, checks: original.Checks, claims: original.Claims, judgments: original.Judgments, err: "ignored"},
		{item: "claim", verdict: VerdictFailed, claims: original.Claims, judgments: original.Judgments, err: "ignored"},
		{item: "error", verdict: VerdictError, err: "broken"},
		{item: "pending", verdict: VerdictPending, claims: []StoredClaim{{ID: "a"}, {ID: "b"}, {ID: "info", Informational: true}}},
		{item: "not-run", verdict: VerdictNotRun},
	} {
		key := AttemptKey{Variant: "base", Scenario: "chat", Item: tc.item, Repeat: 1}
		e.Attempts = append(e.Attempts, &StoredAttempt{AttemptKey: key, Key: key.String(), Index: i, Verdict: tc.verdict, Checks: tc.checks, Claims: tc.claims, Judgments: tc.judgments, Error: tc.err, NotRun: StopTokenLimit})
	}
	var out, data, again bytes.Buffer
	require.NoError(t, Summary(&out, e))
	reportGolden(t, "summary", out.String())
	require.NoError(t, SummaryJSON(&data, e))
	require.NoError(t, SummaryJSON(&again, e))
	assert.Equal(t, data.Bytes(), again.Bytes())
	assert.True(t, jsontext.Value(data.Bytes()).IsValid())
}

func TestReportWriterErrors(t *testing.T) {
	e := loadReportFixture(t)
	require.Error(t, Show(failedReportWriter{}, e, e.Attempts[0], ShowOptions{}))
	require.Error(t, ShowJSON(failedReportWriter{}, e, e.Attempts[0], ShowOptions{}))
	require.Error(t, Summary(failedReportWriter{}, e))
	require.Error(t, SummaryJSON(failedReportWriter{}, e))
}
func (failedReportWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
