package evalcli_test

import (
	"bytes"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShowMatchesStoredReports(t *testing.T) {
	cfg := config(t)
	e := seed(t, cfg)
	for _, tc := range []struct {
		name                        string
		args                        []string
		attempt, json, full, export bool
	}{
		{name: "summary", args: []string{"show", e.Header.ID}},
		{name: "summary json", args: []string{"show", e.Header.ID, "--json"}, json: true},
		{name: "attempt", args: []string{"show", e.Header.ID + "/#0"}, attempt: true},
		{name: "last", args: []string{"show", "last/#0", "--full"}, attempt: true, full: true},
		{name: "json after", args: []string{"show", "last/#0", "--json", "--export"}, attempt: true, json: true, export: true},
		{name: "json before", args: []string{"show", "--json", "last/#0", "--export"}, attempt: true, json: true, export: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var want bytes.Buffer
			var err error
			switch {
			case tc.attempt && tc.json:
				err = eval.ShowJSON(&want, e, e.Attempts[0], eval.ShowOptions{Full: tc.full, Export: tc.export})
			case tc.attempt:
				err = eval.Show(&want, e, e.Attempts[0], eval.ShowOptions{Full: tc.full})
			case tc.json:
				err = eval.SummaryJSON(&want, e)
			default:
				err = eval.Summary(&want, e)
			}
			require.NoError(t, err)
			code, out, diagnostic := invoke(t.Context(), cfg, tc.args...)
			assert.Equal(t, 0, code, diagnostic)
			assert.Equal(t, want.String(), out)
			assert.Empty(t, diagnostic)
		})
	}
}

func TestList(t *testing.T) {
	cfg := config(t)
	seed(t, cfg)
	latest := seed(t, cfg)
	code, out, diagnostic := invoke(t.Context(), cfg, "list", "--json", "--limit", "1")
	require.Equal(t, 0, code, diagnostic)
	var headers []eval.ExperimentHeader
	require.NoError(t, json.Unmarshal([]byte(out), &headers, jsonopts.FormatDurationAsNano(true)))
	require.Len(t, headers, 1)
	assert.Equal(t, latest.Header.ID, headers[0].ID)
	code, out, diagnostic = invoke(t.Context(), cfg, "list", "--limit", "0")
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, out, latest.Header.ID)
	assert.Contains(t, out, "planned")
	assert.Contains(t, out, "revision")
}

func TestUsageErrors(t *testing.T) {
	cfg := config(t)
	e := seed(t, cfg)
	for _, args := range [][]string{
		{"bogus"}, {"show"}, {"show", "unknown-id"}, {"show", e.Header.ID + "/x:y:z"},
		{"show", e.Header.ID, "extra"}, {"show", e.Header.ID, "--bogus"},
		{"list", "--limit", "-1"}, {"list", "--limit", "oops"}, {"list", "extra"},
		{"run", "--scenario", "unknown"}, {"run", "--repeat", "-1"},
	} {
		code, _, diagnostic := invoke(t.Context(), cfg, args...)
		assert.Equal(t, 2, code, "%v: %s", args, diagnostic)
		assert.NotEmpty(t, diagnostic)
	}
	_, headers := experiments(t, cfg.StoreDir)
	assert.Len(t, headers, 1)
	code, _, diagnostic := invoke(t.Context(), evalcli.Config{}, "list")
	assert.Equal(t, 2, code, diagnostic)
}

func TestAmbiguousPrefix(t *testing.T) {
	cfg := config(t)
	// Copy the public persisted fixture twice with fixed IDs sharing a prefix.
	e := seed(t, cfg)
	data, err := os.ReadFile(filepath.Join(e.Dir, "experiment.json"))
	require.NoError(t, err)
	root, err := os.OpenRoot(cfg.StoreDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, root.Close())
	})
	for _, id := range []string{"0199d4a0-0000-7000-8000-000000000001", "0199d4a0-0000-7000-8000-000000000002"} {
		require.NoError(t, root.Mkdir(id, 0700))
		require.NoError(t, root.WriteFile(filepath.Join(id, "experiment.json"), bytes.ReplaceAll(data, []byte(e.Header.ID), []byte(id)), 0600))
	}
	code, _, diagnostic := invoke(t.Context(), cfg, "show", "0199d4a0")
	assert.Equal(t, 2, code)
	assert.Contains(t, diagnostic, "ambiguous")
}

func TestStorePrecedence(t *testing.T) {
	cfg := config(t)
	module := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(module, "go.mod"), []byte("module fixture\n"), 0600))
	cfg.Dir = module
	configured := seed(t, cfg)
	other := config(t)
	explicit := seed(t, other)
	code, out, diagnostic := invoke(t.Context(), cfg, "list", "--out", other.StoreDir)
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, out, explicit.Header.ID)
	assert.NotContains(t, out, configured.Header.ID)
	code, out, diagnostic = invoke(t.Context(), cfg, "list")
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, out, configured.Header.ID)
	cfg.StoreDir = filepath.Join(module, ".loom-evals")
	defaultExperiment := seed(t, cfg)
	cfg.StoreDir = ""
	code, out, diagnostic = invoke(t.Context(), cfg, "list")
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, out, defaultExperiment.Header.ID)
	cfg.Dir = t.TempDir()
	code, _, diagnostic = invoke(t.Context(), cfg, "list")
	assert.Equal(t, 2, code)
	assert.Contains(t, diagnostic, "go.mod")
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"help", "run"}, {"show", "-h"}} {
		cfg := evalcli.Config{Name: "my-evals"}
		code, out, diagnostic := invoke(t.Context(), cfg, args...)
		assert.Equal(t, 0, code, diagnostic)
		assert.Contains(t, out, "my-evals")
		assert.NotContains(t, out, "panic")
		assert.Empty(t, diagnostic)
	}
}
