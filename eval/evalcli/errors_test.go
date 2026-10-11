package evalcli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type panicProgress struct {
	bytes.Buffer
	first bool
}

type failedOutput struct{}

func TestOperationalErrors(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		cfg := config(t)
		e := seed(t, cfg)
		require.NoError(t, os.Remove(filepath.Join(e.Dir, "attempts.jsonl")))
		code, _, diagnostic := invoke(t.Context(), cfg, "show", e.Header.ID)
		assert.Equal(t, 1, code, diagnostic)
	})
	t.Run("resolve IO", func(t *testing.T) {
		cfg := config(t)
		e := seed(t, cfg)
		require.NoError(t, os.WriteFile(filepath.Join(e.Dir, "experiment.json"), []byte("broken"), 0600))
		code, _, diagnostic := invoke(t.Context(), cfg, "show", e.Header.ID)
		assert.Equal(t, 1, code, diagnostic)
	})
	t.Run("default store IO", func(t *testing.T) {
		cfg := config(t)
		cfg.StoreDir = ""
		cfg.Dir = t.TempDir()
		info, err := os.Stat(cfg.Dir)
		require.NoError(t, err)
		require.NoError(t, os.Chmod(cfg.Dir, 0000))
		t.Cleanup(func() {
			assert.NoError(t, os.Chmod(cfg.Dir, info.Mode().Perm()))
		})
		code, _, diagnostic := invoke(t.Context(), cfg, "list")
		assert.Equal(t, 1, code, diagnostic)
	})
	t.Run("compare", func(t *testing.T) {
		cfg := config(t)
		e := seed(t, cfg)
		code, _, diagnostic := invoke(t.Context(), cfg, "compare", e.Header.ID, e.Header.ID)
		assert.Equal(t, 1, code, diagnostic)
	})
	t.Run("stdout", func(t *testing.T) {
		cfg := config(t)
		cfg.Stdout = failedOutput{}
		var diagnostic bytes.Buffer
		cfg.Stderr = &diagnostic
		code := evalcli.Main(t.Context(), []string{"run", "--quiet"}, cfg)
		assert.Equal(t, 1, code, diagnostic.String())
		assert.Contains(t, diagnostic.String(), "output unavailable")
	})
}

func TestRunCallbackPanicStored(t *testing.T) {
	cfg := config(t)
	var out bytes.Buffer
	diagnostic := &panicProgress{first: true}
	cfg.Stdout, cfg.Stderr = &out, diagnostic
	code := evalcli.Main(t.Context(), []string{"run", "--repeat", "2"}, cfg)
	assert.Equal(t, 1, code)
	assert.Contains(t, diagnostic.String(), "progress panic")
	store, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	assert.Equal(t, eval.StopCallbackPanic, headers[0].Stop)
	e, err := store.Load(headers[0].ID)
	require.NoError(t, err)
	assert.True(t, e.Complete())
	assert.Equal(t, eval.VerdictNotRun, e.Attempts[1].Verdict)
}

func TestUsageOnNoOpRerun(t *testing.T) {
	cfg := config(t)
	cfg.Scenarios[0].Items = eval.Items(eval.Item{ID: "pass", Input: "hello"})
	parent := seed(t, cfg)
	for _, flag := range []string{"--repeat", "--concurrency", "--max-tokens"} {
		code, _, diagnostic := invoke(t.Context(), cfg, "run", "--rerun-failed", parent.Header.ID, flag, "-1")
		assert.Equal(t, 2, code, diagnostic)
	}
}

func TestFlagsAnywhere(t *testing.T) {
	cfg := config(t)
	e := seed(t, cfg)
	for _, args := range [][]string{
		{"--out", cfg.StoreDir, "--json", "show", e.Header.ID},
		{"show", "--out", cfg.StoreDir, e.Header.ID, "--json"},
		{"show", e.Header.ID, "--out", cfg.StoreDir, "--json"},
		{"show", "--json", "--", e.Header.ID},
	} {
		code, out, diagnostic := invoke(t.Context(), cfg, args...)
		assert.Equal(t, 0, code, diagnostic)
		assert.Contains(t, out, `"format":"loom-mcp.eval.summary"`)
	}
}

func TestNoOpRerunJSON(t *testing.T) {
	cfg := config(t)
	cfg.Scenarios[0].Items = eval.Items(eval.Item{ID: "pass", Input: "hello"})
	parent := seed(t, cfg)
	code, out, diagnostic := invoke(context.Background(), cfg, "run", "--rerun-failed", parent.Header.ID, "--json")
	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"format":"loom-mcp.eval.run","version":1,"experiment":null,"comparisons":null}`, out)
	assert.Contains(t, diagnostic, "nothing to rerun")
}

func (p *panicProgress) Write(data []byte) (int, error) {
	if p.first {
		p.first = false
		panic("progress panic")
	}
	return p.Buffer.Write(data)
}

func (failedOutput) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}
