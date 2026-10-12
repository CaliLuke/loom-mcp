package evalcli

import (
	"bytes"
	"context"
	"fmt"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/summaryeval"
	"github.com/stretchr/testify/assert"
)

func TestCompactionUsage(t *testing.T) {
	for _, args := range [][]string{{"compaction", "run"}, {"compaction", "run", "--from", "last", "--export", "x"}, {"compaction", "run", "--from", "last", "--variant", "keep=3"}, {"compaction", "run", "--from", "last", "--variant", "unknown=x"}, {"compaction", "run", "--from", "last", "--variant", "prompt=missing"}} {
		var out, err bytes.Buffer
		code := Main(context.Background(), args, Config{Compaction: &summaryeval.Config{}, Stdout: &out, Stderr: &err, StoreDir: t.TempDir()})
		assert.Equal(t, 2, code, err.String())
	}
	var out, err bytes.Buffer
	assert.Equal(t, 0, Main(t.Context(), []string{"compaction", "--help"}, Config{Stdout: &out, Stderr: &err}))
	assert.Contains(t, out.String(), "compaction")
	assert.Equal(t, 2, Main(t.Context(), []string{"compaction", "list"}, Config{Stdout: &out, Stderr: &err}))
}

func TestCompactionGlobalFlagsAndJSONList(t *testing.T) {
	var out, diagnostic bytes.Buffer
	code := Main(t.Context(), []string{"--out", t.TempDir(), "--json", "compaction", "list"}, Config{Compaction: &summaryeval.Config{}, Stdout: &out, Stderr: &diagnostic})
	assert.Equal(t, 0, code, diagnostic.String())
	assert.JSONEq(t, `{"format":"loom-mcp.eval.compaction.list","version":1,"replays":[]}`, out.String())
}

func TestCompactionFactoryGates(t *testing.T) {
	for _, mode := range []string{"models", "judge", "old"} {
		t.Run(mode, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			rec := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "run", ContextFingerprint: "fingerprint", ContextSettings: &compaction.Settings{Prompt: compaction.SummaryPrompt{Template: "template"}}}}}
			if mode == "old" {
				rec.Runs[0].ContextFingerprint = ""
			}
			b, err := rec.Export().Marshal()
			require.NoError(t, err)
			dir := t.TempDir()
			path := filepath.Join(dir, "source.json")
			require.NoError(t, os.WriteFile(path, b, 0600))
			cfg := Config{StoreDir: dir, Compaction: &summaryeval.Config{}, Stdout: &out, Stderr: &diagnostic}
			if mode == "models" {
				cfg.Compaction.NewModels = func() (map[string]summaryeval.Model, error) {
					return nil, fmt.Errorf("wrapped: %w", agenttest.ErrLiveModelDisabled)
				}
			}
			if mode == "judge" {
				cfg.NewJudge = func(judge.Cache) (*judge.Judge, error) {
					return nil, fmt.Errorf("wrapped: %w", agenttest.ErrLiveModelDisabled)
				}
			}
			code := Main(t.Context(), []string{"compaction", "run", "--export", path, "--variant", "prompt=recorded"}, cfg)
			if mode == "old" {
				assert.Equal(t, 1, code)
				assert.Contains(t, diagnostic.String(), "recorded before K17: no pinned summary template")
			} else {
				assert.Equal(t, 0, code, diagnostic.String())
				assert.Contains(t, diagnostic.String(), "disabled")
			}
		})
	}
}
