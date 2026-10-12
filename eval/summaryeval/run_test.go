package summaryeval

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVariants(t *testing.T) {
	cfg := Config{Prompts: map[string]string{"v2": "V2 {{.Previous}} {{.Evicted}}"}, Instructions: map[string]string{"brief": "brief"}}
	for _, tt := range []struct {
		spec     string
		valid    bool
		contains string
	}{
		{"prompt=v2,model=s", true, "model=s,prompt=v2"}, {"prompt=recorded", true, "prompt=recorded"},
		{"keep=5", false, "cut-moving variants are not in K20"}, {"window=50", false, "cut-moving"},
		{"oops=x", false, "unknown"}, {"prompt=missing", false, "unknown prompt"}, {"max_summary_chunks=0", false, "positive"},
	} {
		t.Run(tt.spec, func(t *testing.T) {
			v, err := ParseVariant(cfg, tt.spec)
			if !tt.valid {
				require.ErrorIs(t, err, ErrUsage)
				assert.Contains(t, err.Error(), tt.contains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.contains, v.ID)
		})
	}
}

func TestRunCacheAndCost(t *testing.T) {
	cut := Cuts(recordedFixture(t))[0]
	dir := t.TempDir()
	script, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("new fact"), agenttest.Usage(10, 5)), agenttest.Respond(agenttest.Text("fresh fact"), agenttest.Usage(10, 5)))
	require.NoError(t, err)
	script.WithContextLimits(model.ContextLimits{ContextWindow: 200000})
	cfg := Config{Prompts: map[string]string{"v2": "V2 {{.Previous}} {{.Evicted}}"}, NewModels: func() (map[string]Model, error) {
		return map[string]Model{"summary": {Client: script.Client()}}, nil
	}}
	v, err := ParseVariant(cfg, "prompt=v2")
	require.NoError(t, err)
	opts := Options{Dir: dir, Cuts: []Cut{cut}, Variants: []Variant{v}, Repeat: 1, Now: func() time.Time {
		return time.Unix(42, 0)
	}}
	opts.DryRun = true
	dry, err := Run(t.Context(), cfg, opts)
	require.NoError(t, err)
	assert.Empty(t, script.Requests())
	assert.Equal(t, 3, dry.Preflight.CallUpperBound)
	assert.Positive(t, dry.Preflight.InputTokens)
	opts.DryRun = false
	first, err := Run(t.Context(), cfg, opts)
	require.NoError(t, err)
	require.Len(t, first.Lines, 1)
	assert.Equal(t, "new fact", first.Lines[0].New.Text)
	assert.Equal(t, "verified", first.Lines[0].Verification.Status)
	second, err := Run(t.Context(), cfg, opts)
	require.NoError(t, err)
	assert.True(t, second.Lines[0].New.Cached)
	assert.Len(t, script.Requests(), 1)
	opts.Fresh = true
	third, err := Run(t.Context(), cfg, opts)
	require.NoError(t, err)
	assert.Equal(t, "fresh fact", third.Lines[0].New.Text)
	assert.Len(t, script.Requests(), 2)
	opts.Fresh = false
	opts.MaxTokens = 1
	opts.Repeat = 2
	limited, err := Run(t.Context(), cfg, opts)
	require.NoError(t, err)
	require.Len(t, limited.Lines, 2)
	assert.Equal(t, "token_limit", limited.Lines[1].NotRun)
	loaded, err := Load(dir, first.Header.ID)
	require.NoError(t, err)
	want, err := encode(first.Lines)
	require.NoError(t, err)
	got, err := encode(loaded.Lines)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
	info, err := os.Stat(filepath.Join(dir, "_compaction", first.Header.ID, "replay.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	headers, err := List(dir)
	require.NoError(t, err)
	assert.Len(t, headers, 4)
}

func TestStoreStrictAndTornTail(t *testing.T) {
	cut := Cuts(recordedFixture(t))[0]
	cfg := Config{NewModels: func() (map[string]Model, error) {
		return nil, agenttest.ErrLiveModelDisabled
	}}
	v, err := ParseVariant(cfg, "prompt=recorded")
	require.NoError(t, err)
	dir := t.TempDir()
	result, err := Run(t.Context(), cfg, Options{Dir: dir, Cuts: []Cut{cut}, Variants: []Variant{v}})
	require.NoError(t, err)
	assert.Equal(t, "verified", result.Lines[0].Verification.Status)
	assert.Equal(t, "live_disabled", result.Lines[0].NotRun)
	require.Len(t, result.Header.Variants, 1)
	assert.Equal(t, "prompt=recorded", result.Header.Variants[0].ID)
	assert.NotEmpty(t, result.Header.Variants[0].Fingerprint)
	path := filepath.Join(dir, "_compaction", result.Header.ID, "cuts.jsonl")
	root, err := os.OpenRoot(filepath.Dir(path))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, root.Close())
	}()
	data, err := root.ReadFile(filepath.Base(path))
	require.NoError(t, err)
	for _, tt := range []struct {
		name string
		tail string
		bad  bool
	}{{"torn", "{", false}, {"middle", "{}\n", true}} {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, root.WriteFile(filepath.Base(path), append(append([]byte{}, data...), []byte(tt.tail)...), 0600))
			r, err := Load(dir, result.Header.ID)
			if tt.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.True(t, r.TruncatedTail)
			}
		})
	}
}

func TestRepeatedAttemptRunIDs(t *testing.T) {
	cfg := Config{NewModels: func() (map[string]Model, error) {
		return nil, agenttest.ErrLiveModelDisabled
	}}
	v, err := ParseVariant(cfg, "prompt=recorded")
	require.NoError(t, err)
	cuts := append(Cuts(recordedFixture(t)), Cuts(recordedFixture(t))...)
	dir := t.TempDir()
	e, err := Run(t.Context(), cfg, Options{Dir: dir, Cuts: cuts, Variants: []Variant{v}})
	require.NoError(t, err)
	loaded, err := Load(dir, e.Header.ID)
	require.NoError(t, err)
	assert.Len(t, loaded.Lines, 2)
}
