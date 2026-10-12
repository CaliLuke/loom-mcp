package summaryeval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCutMovingVariantPrefixes(t *testing.T) {
	for _, key := range []string{"min_saving_tokens", "min_saving", "trigger_at_tokens", "trigger_at", "keep_recent_tokens", "protect_recent_tool_tokens", "context_window", "render_budget"} {
		t.Run(key, func(t *testing.T) {
			_, err := ParseVariant(Config{}, key+"=1")
			require.ErrorIs(t, err, ErrUsage)
			assert.ErrorContains(t, err, "cut-moving variants are not in K20")
		})
	}
}

func TestVariantValidationParity(t *testing.T) {
	for _, tt := range []struct{ name, spec, text, kind string }{{"recorded control", "prompt=recorded", "old fact", ""}, {"tail citation", "prompt=v2", "fact [message 2]", "cites_not_behind"}, {"headless custom", "prompt=v2", "headless accepted", ""}} {
		t.Run(tt.name, func(t *testing.T) {
			cut := Cuts(recordedFixture(t))[0]
			script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text(tt.text), agenttest.Usage(10, 5)))
			require.NoError(t, err)
			script.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
			cfg := Config{Prompts: map[string]string{"v2": "V2 {{.Previous}} {{.Evicted}}"}, NewModels: func() (map[string]Model, error) {
				return map[string]Model{"summary": {Client: script.Client()}}, nil
			}}
			v, err := ParseVariant(cfg, tt.spec)
			require.NoError(t, err)
			result, err := Run(t.Context(), cfg, Options{Dir: t.TempDir(), Cuts: []Cut{cut}, Variants: []Variant{v}})
			require.NoError(t, err)
			assert.Equal(t, tt.kind, result.Lines[0].New.Kind)
			if tt.spec == "prompt=recorded" {
				assert.Equal(t, cut.Record, result.Lines[0].New.Checkpoint)
			}
		})
	}
}

func TestCacheIdentityAndDeterminism(t *testing.T) {
	cut := Cuts(recordedFixture(t))[0]
	dir := t.TempDir()
	turns := make([]agenttest.Turn, 0, 8)
	for range 8 {
		turns = append(turns, agenttest.Respond(agenttest.Text("same fact")))
	}
	script, err := agenttest.NewScript(turns...)
	require.NoError(t, err)
	script.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
	cfg := Config{Prompts: map[string]string{"v2": "V2 {{.Evicted}}"}, NewModels: func() (map[string]Model, error) {
		return map[string]Model{"summary": {Client: script.Client()}, "other": {Client: script.Client()}}, nil
	}}
	run := func(spec string, repeat int, fresh bool) *Experiment {
		t.Helper()
		v, err := ParseVariant(cfg, spec)
		require.NoError(t, err)
		e, err := Run(t.Context(), cfg, Options{Dir: dir, Cuts: []Cut{cut}, Variants: []Variant{v}, Repeat: repeat, Fresh: fresh, Now: func() time.Time {
			return time.Unix(123, 0)
		}})
		require.NoError(t, err)
		return e
	}
	first := run("prompt=v2", 1, false)
	second := run("prompt=v2", 1, false)
	assert.False(t, first.Lines[0].New.Cached)
	assert.True(t, second.Lines[0].New.Cached)
	assert.Len(t, script.Requests(), 1)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, root.Close())
	}()
	cold, err := root.ReadFile(filepath.Join("_compaction", first.Header.ID, "cuts.jsonl"))
	require.NoError(t, err)
	hit, err := root.ReadFile(filepath.Join("_compaction", second.Header.ID, "cuts.jsonl"))
	require.NoError(t, err)
	// Cache provenance must differ. All persisted evidence must remain exact;
	// cuts.jsonl has only an ID in its header (timestamps live in replay.json).
	normalized := strings.Replace(string(hit), second.Header.ID, first.Header.ID, 1)
	normalized = strings.ReplaceAll(normalized, `"cached":true`, `"cached":false`)
	assert.Equal(t, string(cold), normalized)
	second = run("prompt=v2", 1, true)
	a, err := encode(first.Lines)
	require.NoError(t, err)
	b, err := encode(second.Lines)
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b))
	cachedRun := run("prompt=v2", 1, false)
	assert.True(t, cachedRun.Lines[0].New.Cached)
	assert.Len(t, script.Requests(), 2)
	run("prompt=v2,model=other", 1, false)
	assert.Len(t, script.Requests(), 3)
	cfg.Prompts["v2"] += "!"
	run("prompt=v2", 2, false)
	assert.Len(t, script.Requests(), 5)
	paths, err := filepath.Glob(filepath.Join(dir, "_compaction", "cache", "*.json"))
	require.NoError(t, err)
	for _, p := range paths {
		require.NoError(t, os.WriteFile(p, []byte("corrupt"), 0600))
	}
	run("prompt=v2", 1, false)
	assert.Len(t, script.Requests(), 6)
}

func TestStoreHeaderStrict(t *testing.T) {
	cfg := Config{NewModels: func() (map[string]Model, error) {
		return nil, agenttest.ErrLiveModelDisabled
	}}
	v, err := ParseVariant(cfg, "prompt=recorded")
	require.NoError(t, err)
	dir := t.TempDir()
	e, err := Run(t.Context(), cfg, Options{Dir: dir, Cuts: Cuts(recordedFixture(t)), Variants: []Variant{v}})
	require.NoError(t, err)
	path := filepath.Join(dir, "_compaction", e.Header.ID, "replay.json")
	root, err := os.OpenRoot(filepath.Dir(path))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, root.Close())
	}()
	original, err := root.ReadFile(filepath.Base(path))
	require.NoError(t, err)
	for _, tt := range []struct{ name, body string }{{"unknown", strings.Replace(string(original), "{", "{\"extra\":true,", 1)}, {"version", strings.Replace(string(original), "\"version\":1", "\"version\":2", 1)}, {"format", strings.Replace(string(original), replayFormat, "other", 1)}} {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, root.WriteFile(filepath.Base(path), []byte(tt.body), 0600))
			_, err := Load(dir, e.Header.ID)
			require.Error(t, err)
		})
	}
}
