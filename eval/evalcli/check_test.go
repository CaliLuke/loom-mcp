package evalcli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckStoredStrictWithoutHarness(t *testing.T) {
	cfg := config(t)
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--item", "pass")
	require.Equal(t, 0, code, diagnostic)
	store, err := eval.Open(cfg.StoreDir)
	require.NoError(t, err)
	id, err := store.Resolve("last")
	require.NoError(t, err)
	cfg.Harness, cfg.Scenarios = nil, nil
	code, out, diagnostic := invoke(t.Context(), cfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, out, "1/1 passed")
	assert.Contains(t, out, "strict")
	code, out, diagnostic = invoke(t.Context(), cfg, "--json", "check", id, "--policy", "strict")
	assert.Equal(t, 0, code, diagnostic)
	var result eval.QualityCheck
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.True(t, result.Passed)
	assert.Equal(t, eval.QualityCounts{Passed: 1}, result.Counts)
}

func TestCheckRejectedAndRunRemainsZero(t *testing.T) {
	cfg := config(t)
	code, _, diagnostic := invoke(t.Context(), cfg, "run")
	require.Equal(t, 0, code, diagnostic)
	cfg.Harness, cfg.Scenarios = nil, nil
	code, out, diagnostic := invoke(t.Context(), cfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 3, code, diagnostic)
	assert.Contains(t, out, "1 failed")
	assert.Contains(t, out, "failed_verdict")
	code, out, diagnostic = invoke(t.Context(), cfg, "check", "--policy", "strict", "--json", "last")
	assert.Equal(t, 3, code, diagnostic)
	var result eval.QualityCheck
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.False(t, result.Passed)
	assert.Equal(t, eval.QualityCounts{Passed: 1, Failed: 1}, result.Counts)
}

func TestCheckUsageAndStorageErrors(t *testing.T) {
	cfg := config(t)
	cfg.Harness, cfg.Scenarios = nil, nil
	for _, args := range [][]string{
		{"check", "last"},
		{"check", "--policy", "strict"},
		{"check", "last", "--policy", "lenient"},
		{"check", "a", "b", "--policy", "strict"},
	} {
		code, _, _ := invoke(t.Context(), cfg, args...)
		assert.Equal(t, 2, code, args)
	}
	code, _, _ := invoke(t.Context(), cfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 2, code) // Unknown reference in a valid empty store.

	seedCfg := config(t)
	e := seed(t, seedCfg)
	seedCfg.Harness, seedCfg.Scenarios = nil, nil
	header := filepath.Join(e.Dir, "experiment.json")
	require.NoError(t, os.WriteFile(header, []byte("{"), 0600))
	code, _, diagnostic := invoke(t.Context(), seedCfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, diagnostic)
}

func TestCheckMalformedAttemptAndTornTail(t *testing.T) {
	cfg := config(t)
	e := seed(t, cfg)
	cfg.Harness, cfg.Scenarios = nil, nil
	root, err := os.OpenRoot(e.Dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	original, err := root.ReadFile("attempts.jsonl")
	require.NoError(t, err)
	rewriteAttempts(t, root, append(append([]byte{}, original...), '{'))
	code, out, diagnostic := invoke(t.Context(), cfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 3, code, diagnostic)
	assert.Contains(t, out, "truncated_tail")
	rewriteAttempts(t, root, append(append([]byte{}, original...), []byte("{bad}\n")...))
	code, _, diagnostic = invoke(t.Context(), cfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, diagnostic)
	require.NoError(t, root.Remove("attempts.jsonl"))
	code, _, diagnostic = invoke(t.Context(), cfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, diagnostic)
}

func rewriteAttempts(t *testing.T, root *os.Root, data []byte) {
	t.Helper()
	f, err := root.OpenFile("attempts.jsonl", os.O_WRONLY|os.O_TRUNC, 0600)
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestCheckOutputFailureIsOperational(t *testing.T) {
	cfg := config(t)
	seed(t, cfg)
	cfg.Harness, cfg.Scenarios = nil, nil
	cfg.Stdout = errWriter{}
	cfg.Stderr = &bytes.Buffer{}
	assert.Equal(t, 1, evalcli.Main(t.Context(), []string{"check", "last", "--policy", "strict"}, cfg))
}

func TestCheckCannotBeShadowedByExtension(t *testing.T) {
	cfg := evalcli.Config{Extensions: []evalcli.Extension{{Name: "check", Run: func(context.Context, evalcli.ExtensionRequest) error {
		return nil
	}}}}
	code, _, diagnostic := invoke(t.Context(), cfg, "check", "last", "--policy", "strict")
	assert.Equal(t, 2, code)
	assert.Contains(t, diagnostic, "shadows built-in")
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("output failed")
}

func TestCheckProbeQualityIsUnassessed(t *testing.T) {
	cfg := config(t)
	store, err := eval.Open(cfg.StoreDir)
	require.NoError(t, err)
	probe := eval.ProbeConfig{Key: eval.AttemptKey{Variant: "base", Scenario: "probe", Item: "one", Repeat: 1}, Item: eval.Item{ID: "one", Input: "task"}, Variant: eval.Variant{ID: "base"}, PrepareTimeout: time.Second, CleanupTimeout: time.Second, PrepareAttempt: func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		t.Fatal("read command invoked preparation")
		return nil, nil
	}}
	writer, err := store.BeginProbe(t.Context(), probe, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	cfg.Harness, cfg.Scenarios = nil, nil
	code, out, diagnostic := invoke(t.Context(), cfg, "check", writer.ID(), "--policy", "strict")
	assert.Equal(t, 3, code, diagnostic)
	assert.Contains(t, out, "quality not_assessed")
	assert.Contains(t, out, "quality_not_assessed")
	assert.NotContains(t, out, "0/0 passed")
	code, out, diagnostic = invoke(t.Context(), cfg, "check", writer.ID(), "--policy", "strict", "--json")
	assert.Equal(t, 3, code, diagnostic)
	var result eval.QualityCheck
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.False(t, result.Passed)
	assert.Equal(t, eval.ExecutionProbe, result.ExecutionKind)
	assert.Contains(t, result.Reasons, "quality_not_assessed")
}
