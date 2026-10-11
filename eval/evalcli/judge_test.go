package evalcli_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunJudge(t *testing.T) {
	for _, tc := range []struct {
		name        string
		noJudge     bool
		factoryErr  error
		code, calls int
		verdict     eval.Verdict
		notice      string
	}{
		{name: "wired", calls: 1, verdict: eval.VerdictPassed},
		{name: "no judge", noJudge: true, verdict: eval.VerdictPending},
		{name: "live disabled", factoryErr: fmt.Errorf("factory: %w", agenttest.ErrLiveModelDisabled), calls: 1, verdict: eval.VerdictPending, notice: "LOOM_LIVE_MODEL=1"},
		{name: "factory error", factoryErr: errors.New("judge unavailable"), code: 1, calls: 1, notice: "judge unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOOM_LIVE_MODEL", "")
			cfg := config(t)
			cfg.Scenarios[0].Items = eval.Items(eval.Item{ID: "pass", Input: "hello"})
			cfg.Scenarios[0].Claims = []eval.Claim{{ID: "helpful", Text: "The answer is helpful."}}
			cfg.Scenarios[0].Drive = func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
				return &agenttest.SessionResult{Trajectory: trajectory.Session{{Final: &model.Message{Parts: []model.Part{model.TextPart{Text: "Hello!"}}}}}}, nil
			}
			calls := 0
			cfg.NewJudge = func(cache judge.Cache) (*judge.Judge, error) {
				calls++
				if tc.factoryErr != nil {
					return nil, tc.factoryErr
				}
				client, _ := agenttest.ScriptClient(t,
					judgeTurn("arrival", "entailed"), judgeTurn("arrival", "contradicted"),
					judgeTurn("arrival", "not_addressed"), judgeTurn("arrival", "indeterminate"),
					judgeTurn("helpful", "entailed"),
				)
				return judge.New(judge.Config{Client: client, ModelID: "scripted-judge", MaxOutputTokens: 512, Cache: cache})
			}
			// --out must select both the experiment store and its calibration cache.
			storeDir := t.TempDir()
			args := []string{"run", "--out", storeDir, "--quiet"}
			if tc.noJudge {
				args = append(args, "--no-judge")
			}

			code, _, diagnostic := invoke(t.Context(), cfg, args...)

			assert.Equal(t, tc.code, code, diagnostic)
			assert.Equal(t, tc.calls, calls)
			assert.Contains(t, diagnostic, tc.notice)
			store, headers := experiments(t, storeDir)
			if tc.code != 0 {
				assert.Empty(t, headers, "factory failure must precede Begin")
				return
			}
			require.Len(t, headers, 1)
			e, err := store.Load(headers[0].ID)
			require.NoError(t, err)
			require.Len(t, e.Attempts, 1)
			assert.Equal(t, tc.verdict, e.Attempts[0].Verdict)
			assert.Equal(t, "complete", e.Header.Status)
			if tc.verdict == eval.VerdictPending {
				assert.Empty(t, e.Attempts[0].Judgments)
				assert.Nil(t, e.Header.Judge)
				return
			}
			require.Len(t, e.Attempts[0].Judgments, 1)
			assert.Equal(t, "entailed", e.Attempts[0].Judgments[0].Label)
			require.NotNil(t, e.Header.Judge)
			assert.Equal(t, "scripted-judge", e.Header.Judge.ModelID)
			assert.True(t, e.Header.Judge.Calibrated)
			cache, err := judge.NewDirCache(filepath.Join(storeDir, "_calibration"))
			require.NoError(t, err)
			calibration, found := cache.Load(e.Header.Judge.CalibrationKey)
			require.True(t, found, "the supplied cache must persist under the selected store")
			assert.True(t, calibration.Passed)
			_, err = os.Stat(filepath.Join(cfg.StoreDir, "_calibration"))
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func judgeTurn(id, label string) agenttest.Turn {
	return agenttest.Respond(agenttest.RawToolCall("record_judgments", rawjson.Message(fmt.Sprintf(`{%q:{"label":%q,"rationale":"scripted evidence"}}`, id, label))))
}
