package evalcli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/summaryeval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unavailableCompactionOutput struct{}

func TestCompactionCLIRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"}, {"run", "--unknown"}, {"show"}, {"show", "a", "b"}, {"list", "unexpected"},
		{"list", "--limit=-1"}, {"run", "--from=last", "--variant=prompt=recorded", "--repeat=0"},
		{"run", "--from=last", "--variant=prompt=recorded", "--concurrency=0"},
		{"run", "--from=last", "--variant=prompt=recorded", "--max-tokens=-1"},
		{"run", "--from=last", "--variant=prompt=recorded", "--cuts=unknown"},
	} {
		t.Run(args[0]+"/"+args[len(args)-1], func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			cfg := Config{StoreDir: t.TempDir(), Compaction: &summaryeval.Config{}, Stdout: &out, Stderr: &diagnostic}
			assert.Equal(t, 2, Main(t.Context(), append([]string{"compaction"}, args...), cfg), diagnostic.String())
			assert.NotEmpty(t, diagnostic.String())
		})
	}
}

func TestCompactionCLIHelpAndReadFailures(t *testing.T) {
	for _, command := range []string{"run", "show", "list"} {
		t.Run(command+" help", func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			cfg := Config{Stdout: &out, Stderr: &diagnostic}
			assert.Zero(t, Main(t.Context(), []string{"compaction", command, "--help"}, cfg))
			assert.Contains(t, out.String(), "-out")
		})
	}
	for _, args := range [][]string{{"show", "missing"}, {"run", "--export=missing.json", "--variant=prompt=recorded"}, {"list"}} {
		t.Run(args[0]+" read failure", func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			dir := t.TempDir()
			if args[0] == "list" {
				dir = filepath.Join(dir, "file")
				require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0600))
			}
			cfg := Config{StoreDir: dir, Compaction: &summaryeval.Config{}, Stdout: &out, Stderr: &diagnostic}
			assert.NotZero(t, Main(t.Context(), append([]string{"compaction"}, args...), cfg))
			assert.NotEmpty(t, diagnostic.String())
		})
	}
	var diagnostic bytes.Buffer
	assert.Equal(t, 1, Main(t.Context(), []string{"compaction", "help"}, Config{Stdout: unavailableCompactionOutput{}, Stderr: &diagnostic}))
	assert.Contains(t, diagnostic.String(), "output unavailable")
}

func (unavailableCompactionOutput) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}
