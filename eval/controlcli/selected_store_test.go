package controlcli_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/controlcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
)

func TestControlCLIReopensCandidateStateBesideSelectedStore(t *testing.T) {
	for _, tt := range []struct {
		name       string
		configured string
		prefix     []string
		suffix     []string
		selected   string
	}{
		{name: "default", selected: ".loom-evals"},
		{name: "configured", configured: "configured", selected: "configured"},
		{name: "out before command", configured: "fallback", prefix: []string{"--out", "selected"}, selected: "selected"},
		{name: "out after command", configured: "fallback", suffix: []string{"--out", "selected"}, selected: "selected"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			working, err := os.Getwd()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile("go.mod", []byte("module example.com/consumer\n"), 0600))
			require.NoError(t, os.Mkdir("cmd", 0700))
			selected := filepath.Join(working, tt.selected)
			cfg := cliControlConfig(t)
			discovery := cfg.Candidates.Discover()
			baseline, err := cfg.Candidates.Load(t.Context(), discovery.BaselineDigest)
			require.NoError(t, err)
			var boundDirs []string
			extensions, err := controlcli.Extensions(func(ctx context.Context, store *eval.Store) (*control.Service, error) {
				boundDirs = append(boundDirs, store.Dir())
				candidates, err := candidate.Open(ctx, store.Dir()+"-candidates", discovery.Surface, baseline.Components())
				if err != nil {
					return nil, err
				}
				bound := cfg
				bound.Candidates, bound.Experiments = candidates, store
				return control.New(bound)
			})
			require.NoError(t, err)
			var out, diagnostic bytes.Buffer
			cli := evalcli.Config{Dir: filepath.Join(working, "cmd"), StoreDir: tt.configured, Extensions: extensions, Stdout: &out, Stderr: &diagnostic}
			create := controlJSON(t, control.CreateRequest{Baseline: discovery.BaselineDigest})
			args := append([]string{}, tt.prefix...)
			args = append(args, "experiment", "create_candidate", "--request", create)
			args = append(args, tt.suffix...)
			require.Equal(t, 0, evalcli.Main(t.Context(), args, cli), diagnostic.String())
			var created control.CandidateInfo
			require.NoError(t, json.Unmarshal(out.Bytes(), &created))
			require.NotEmpty(t, created.DraftID)
			assert.DirExists(t, selected+"-candidates")

			// A new invocation reopens both stores using only the selected eval path.
			out.Reset()
			diagnostic.Reset()
			get := controlJSON(t, control.GetCandidateRequest{DraftID: created.DraftID})
			args = []string{"--out", selected, "experiment", "get_candidate", "--request", get}
			require.Equal(t, 0, evalcli.Main(t.Context(), args, cli), diagnostic.String())
			var reopened control.CandidateInfo
			require.NoError(t, json.Unmarshal(out.Bytes(), &reopened))
			assert.Equal(t, created, reopened)
			assert.Equal(t, []string{selected, selected}, boundDirs)
			assert.NotContains(t, out.String(), selected)
		})
	}
}
