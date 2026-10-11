package evalcli_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCompareLast(t *testing.T) {
	cfg := config(t)
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--quiet")
	require.Equal(t, 0, code, diagnostic)
	_, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	baseline := headers[0].ID
	code, out, diagnostic := invoke(t.Context(), cfg, "run", "--compare", "last", "--json", "--quiet")
	require.Equal(t, 0, code, diagnostic)
	var envelope struct {
		Comparisons jsontext.Value `json:"comparisons"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &envelope))
	var compared struct {
		Comparisons []struct {
			Baseline  eval.SideInfo `json:"baseline"`
			Candidate eval.SideInfo `json:"candidate"`
		} `json:"comparisons"`
	}
	require.NoError(t, json.Unmarshal(envelope.Comparisons, &compared))
	require.Len(t, compared.Comparisons, 1)
	assert.Equal(t, baseline, compared.Comparisons[0].Baseline.ID)
	assert.NotEqual(t, baseline, compared.Comparisons[0].Candidate.ID)
	cfg.Scenarios[0].Items = eval.Items(eval.Item{ID: "disjoint", Input: "hello"})
	code, out, diagnostic = invoke(t.Context(), cfg, "run", "--compare", "last", "--json", "--quiet")
	assert.Equal(t, 0, code)
	assert.Contains(t, diagnostic, "no overlapping experiment")
	require.NoError(t, json.Unmarshal([]byte(out), &envelope))
	assert.Equal(t, "null", string(envelope.Comparisons))
}

func TestCompareCommands(t *testing.T) {
	cfg := config(t)
	cfg.Variants = []eval.Variant{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	base := seed(t, cfg)
	cand := seed(t, cfg)
	comparisons, err := eval.CompareExperiments(base, cand, eval.CompareOptions{})
	require.NoError(t, err)
	variants, err := eval.CompareVariants(cand, []string{"a", "b", "c"})
	require.NoError(t, err)
	for _, tc := range []struct {
		name        string
		args        []string
		comparisons []*eval.Comparison
	}{
		{"experiments", []string{"compare", base.Header.ID, cand.Header.ID}, comparisons},
		{"variants", []string{"compare", cand.Header.ID, "--variants", "a,b,c"}, variants},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var want bytes.Buffer
			require.NoError(t, eval.WriteComparisons(&want, tc.comparisons))
			code, out, diagnostic := invoke(t.Context(), cfg, tc.args...)
			assert.Equal(t, 0, code, diagnostic)
			assert.Equal(t, want.String(), out)
			want.Reset()
			require.NoError(t, eval.WriteComparisonsJSON(&want, tc.comparisons))
			code, out, diagnostic = invoke(t.Context(), cfg, append(tc.args, "--json")...)
			assert.Equal(t, 0, code, diagnostic)
			assert.Equal(t, want.String(), out)
		})
	}
}

func TestMergeParent(t *testing.T) {
	cfg := config(t)
	parent := seed(t, cfg)
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--rerun-failed", parent.Header.ID, "--compare", parent.Header.ID, "--merge-parent", "--quiet")
	require.Equal(t, 0, code, diagnostic)
	store, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 2)
	cand, err := store.Load(headers[0].ID)
	require.NoError(t, err)
	comparisons, err := eval.CompareExperiments(parent, cand, eval.CompareOptions{Parent: parent})
	require.NoError(t, err)
	var want bytes.Buffer
	require.NoError(t, eval.WriteComparisonsJSON(&want, comparisons))
	code, out, diagnostic := invoke(t.Context(), cfg, "compare", parent.Header.ID, cand.Header.ID, "--merge-parent", "--json")
	assert.Equal(t, 0, code, diagnostic)
	assert.Equal(t, want.String(), out)
}

func TestLiveDisabledBaselineNotice(t *testing.T) {
	t.Setenv("LOOM_LIVE_MODEL", "")
	cfg := config(t)
	// A pre-canceled run is not sufficient: preserve the precise live stop reason.
	cfg.Harness = disabledFactory
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--quiet")
	require.Equal(t, 1, code, diagnostic)
	good := config(t)
	good.StoreDir = cfg.StoreDir
	code, _, diagnostic = invoke(t.Context(), good, "run", "--compare", "last", "--quiet")
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, diagnostic, "live_model_disabled")
}

func disabledFactory(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
	return nil, agenttest.ErrLiveModelDisabled
}
