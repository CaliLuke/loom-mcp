package evalcli_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type progressObserver struct {
	t     *testing.T
	dir   string
	lines int
}

func TestRunProgressAndVerdicts(t *testing.T) {
	cfg := config(t)
	observer := &progressObserver{t: t, dir: cfg.StoreDir}
	var out bytes.Buffer
	cfg.Stdout, cfg.Stderr = &out, observer
	code := evalcli.Main(t.Context(), []string{"run", "--scenario", "s", "--repeat", "2", "--out", cfg.StoreDir}, cfg)
	require.Equal(t, 0, code)
	assert.Equal(t, 4, observer.lines)
	assert.Less(t, strings.Index(out.String(), "s/fail ["), strings.Index(out.String(), "s/pass ["))
	_, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	assert.Equal(t, "complete", headers[0].Status)
	assert.Equal(t, 4, headers[0].Planned)
}

func TestRunJSON(t *testing.T) {
	cfg := config(t)
	code, out, diagnostic := invoke(t.Context(), cfg, "run", "--json", "--quiet", "--repeat", "2", "--concurrency", "2")
	require.Equal(t, 0, code, diagnostic)
	assert.Empty(t, diagnostic)
	var envelope struct {
		Format      string         `json:"format"`
		Version     int            `json:"version"`
		Experiment  jsontext.Value `json:"experiment"`
		Comparisons jsontext.Value `json:"comparisons"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &envelope))
	assert.Equal(t, "loom-mcp.eval.run", envelope.Format)
	assert.Equal(t, 1, envelope.Version)
	assert.Equal(t, "null", string(envelope.Comparisons))
	store, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	e, err := store.Load(headers[0].ID)
	require.NoError(t, err)
	var want bytes.Buffer
	require.NoError(t, eval.SummaryJSON(&want, e))
	assert.JSONEq(t, want.String(), string(envelope.Experiment))
	report, err := eval.Run(t.Context(), eval.Config{Harness: cfg.Harness, Scenarios: cfg.Scenarios, Repeats: 2})
	require.NoError(t, err)
	var summary struct {
		Groups []struct {
			Variant  string `json:"variant"`
			Scenario string `json:"scenario"`
			Item     string `json:"item"`
			Attempts int    `json:"attempts"`
			Passed   int    `json:"passed"`
			Failed   int    `json:"failed"`
			Errored  int    `json:"errored"`
			Pending  int    `json:"pending"`
			NotRun   int    `json:"not_run"`
		} `json:"groups"`
	}
	require.NoError(t, json.Unmarshal(envelope.Experiment, &summary))
	groups := make([]eval.Group, len(summary.Groups))
	for i, g := range summary.Groups {
		groups[i] = eval.Group{Variant: g.Variant, Scenario: g.Scenario, Item: g.Item, Attempts: g.Attempts, Passed: g.Passed, Failed: g.Failed, Errored: g.Errored, Pending: g.Pending, NotRun: g.NotRun}
	}
	assert.ElementsMatch(t, report.Groups(), groups)
}

func TestRunSelectors(t *testing.T) {
	for _, tc := range []struct {
		flag, first, second string
		want                eval.Selection
	}{
		{"--variant", "v1", "v2", eval.Selection{Variants: []string{"v1", "v2"}}},
		{"--scenario", "s", "other", eval.Selection{Scenarios: []string{"s", "other"}}},
		{"--tag", "red", "blue", eval.Selection{Tags: []string{"red", "blue"}}},
		{"--item", "s/pass", "other/fail", eval.Selection{Items: []string{"s/pass", "other/fail"}}},
	} {
		for _, comma := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/comma=%t", tc.flag, comma), func(t *testing.T) {
				cfg := config(t)
				cfg.Variants = []eval.Variant{{ID: "v1"}, {ID: "v2"}}
				second := cfg.Scenarios[0]
				second.ID, second.Tags = "other", []string{"blue"}
				cfg.Scenarios = append(cfg.Scenarios, second)
				args := []string{"run", "--quiet", tc.flag, tc.first, tc.flag, tc.second}
				if comma {
					args = []string{"run", "--quiet", tc.flag, tc.first + "," + tc.second}
				}
				code, _, diagnostic := invoke(t.Context(), cfg, args...)
				require.Equal(t, 0, code, diagnostic)
				store, headers := experiments(t, cfg.StoreDir)
				require.Len(t, headers, 1)
				got := headers[0].Config.Selection
				assert.Equal(t, append([]string{}, tc.want.Variants...), got.Variants)
				assert.Equal(t, append([]string{}, tc.want.Scenarios...), got.Scenarios)
				assert.Equal(t, append([]string{}, tc.want.Tags...), got.Tags)
				assert.Equal(t, append([]string{}, tc.want.Items...), got.Items)
				keys, err := eval.Plan(eval.Config{Harness: cfg.Harness, Scenarios: cfg.Scenarios, Variants: cfg.Variants, Select: tc.want})
				require.NoError(t, err)
				e, err := store.Load(headers[0].ID)
				require.NoError(t, err)
				actual := make([]eval.AttemptKey, len(e.Attempts))
				for i, a := range e.Attempts {
					actual[i] = a.AttemptKey
				}
				assert.Equal(t, keys, actual)
			})
		}
		t.Run(tc.flag+" unknown", func(t *testing.T) {
			cfg := config(t)
			code, _, diagnostic := invoke(t.Context(), cfg, "run", tc.flag, "unknown")
			assert.Equal(t, 2, code, diagnostic)
			_, headers := experiments(t, cfg.StoreDir)
			assert.Empty(t, headers)
		})
	}
}

func TestRerun(t *testing.T) {
	cfg := config(t)
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--repeat", "3", "--quiet")
	require.Equal(t, 0, code, diagnostic)
	_, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	parent := headers[0].ID
	code, _, diagnostic = invoke(t.Context(), cfg, "run", "--rerun-failed", parent, "--quiet")
	require.Equal(t, 0, code, diagnostic)
	store, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 2)
	e, err := store.Load(headers[0].ID)
	require.NoError(t, err)
	assert.Equal(t, parent, e.Header.Parent)
	assert.Equal(t, 3, e.Header.Config.Repeats)
	require.Len(t, e.Attempts, 3)
	for i, a := range e.Attempts {
		assert.Equal(t, "fail", a.Item)
		assert.Equal(t, i+1, a.Repeat)
	}
	code, _, diagnostic = invoke(t.Context(), cfg, "run", "--rerun-failed", parent, "--repeat", "1", "--quiet")
	require.Equal(t, 0, code, diagnostic)
	_, headers = experiments(t, cfg.StoreDir)
	assert.Equal(t, 1, headers[0].Config.Repeats)
	for _, selector := range []string{"--variant", "--scenario", "--tag", "--item"} {
		code, _, diagnostic = invoke(t.Context(), cfg, "run", "--rerun-failed", parent, selector, "anything")
		assert.Equal(t, 2, code, diagnostic)
	}
	_, headers = experiments(t, cfg.StoreDir)
	assert.Len(t, headers, 3)
}

func TestRerunAllPassed(t *testing.T) {
	cfg := config(t)
	cfg.Scenarios[0].Items = eval.Items(eval.Item{ID: "pass", Input: "hi"})
	parent := seed(t, cfg)
	code, out, diagnostic := invoke(t.Context(), cfg, "run", "--rerun-failed", parent.Header.ID)
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, out, "nothing to rerun")
	_, headers := experiments(t, cfg.StoreDir)
	assert.Len(t, headers, 1)
}

func TestRunCancellation(t *testing.T) {
	cfg := config(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg.Scenarios[0].Drive = func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
		cancel()
		return &agenttest.SessionResult{}, nil
	}
	code, _, diagnostic := invoke(ctx, cfg, "run", "--quiet", "--repeat", "2")
	assert.Equal(t, 1, code, diagnostic)
	store, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	assert.Equal(t, "stopped", headers[0].Status)
	assert.Equal(t, eval.StopCanceled, headers[0].Stop)
	e, err := store.Load(headers[0].ID)
	require.NoError(t, err)
	assert.True(t, e.Complete())
	assert.Equal(t, eval.VerdictNotRun, e.Attempts[1].Verdict)
}

func TestRunLiveGate(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("LOOM_LIVE_MODEL", "2")
		cfg := config(t)
		calls := 0
		cfg.Harness = func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
			calls++
			return nil, nil
		}
		code, _, diagnostic := invoke(t.Context(), cfg, "run")
		assert.Equal(t, 2, code)
		assert.Contains(t, diagnostic, "LOOM_LIVE_MODEL")
		assert.Zero(t, calls)
		entries, err := os.ReadDir(cfg.StoreDir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("disabled", func(t *testing.T) {
		t.Setenv("LOOM_LIVE_MODEL", "")
		cfg := config(t)
		cfg.Variants = []eval.Variant{{ID: "live", Options: []agenttest.Option{agenttest.LiveModel("fixture")}}}
		code, _, diagnostic := invoke(t.Context(), cfg, "run")
		assert.Equal(t, 1, code)
		assert.Contains(t, diagnostic, "live model disabled: set LOOM_LIVE_MODEL=1 to run live variants")
		store, headers := experiments(t, cfg.StoreDir)
		require.Len(t, headers, 1)
		assert.Equal(t, "stopped", headers[0].Status)
		assert.Equal(t, eval.StopLiveModelDisabled, headers[0].Stop)
		e, err := store.Load(headers[0].ID)
		require.NoError(t, err)
		assert.Equal(t, eval.VerdictError, e.Attempts[0].Verdict)
		assert.Equal(t, eval.VerdictNotRun, e.Attempts[1].Verdict)
	})
}

func TestStoreWriteFailureCancelsRun(t *testing.T) {
	cfg := config(t)
	var runCtx context.Context
	calls := 0
	cfg.Scenarios[0].Drive = func(ctx context.Context, _ *agenttest.Harness, _ eval.Item) (*agenttest.SessionResult, error) {
		runCtx = ctx
		calls++
		store, err := eval.Open(cfg.StoreDir)
		if err != nil {
			return nil, err
		}
		headers, err := store.List()
		if err != nil {
			return nil, err
		}
		if len(headers) != 1 {
			return nil, fmt.Errorf("expected one begun experiment, got %d", len(headers))
		}
		path := filepath.Join(cfg.StoreDir, headers[0].ID, "attempts.jsonl")
		if err := os.Chmod(path, 0400); err != nil {
			return nil, err
		}
		t.Cleanup(func() { assert.NoError(t, os.Chmod(path, 0600)) })
		return &agenttest.SessionResult{}, nil
	}
	code, out, diagnostic := invoke(t.Context(), cfg, "run", "--repeat", "3")
	assert.Equal(t, 1, code, diagnostic)
	assert.Equal(t, 1, calls)
	require.NotNil(t, runCtx)
	require.ErrorIs(t, runCtx.Err(), context.Canceled)
	assert.Contains(t, diagnostic, "permission denied")
	assert.Contains(t, out, "not run")
	assert.NotContains(t, diagnostic, "[1/")
}

func (p *progressObserver) Write(data []byte) (int, error) {
	p.t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		p.lines++
		store, headers := experiments(p.t, p.dir)
		require.Len(p.t, headers, 1)
		e, err := store.Load(headers[0].ID)
		require.NoError(p.t, err)
		assert.Len(p.t, e.Attempts, p.lines, "progress must follow durable append")
		assert.Regexp(p.t, `^\[\d+/4\] (passed|failed) default:s:(pass|fail):[12] \S+ 0 tok$`, line)
		assert.NotContains(p.t, line, "\r")
		assert.NotContains(p.t, line, "\x1b")
	}
	return len(data), nil
}
