package opik_test

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/features/opik"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpikExtensionSelectsStoreWithoutNetwork(t *testing.T) {
	tests := []struct {
		name string
		args func(string) []string
	}{
		{"before command", func(dir string) []string {
			return []string{"--out", dir, "opik-export", "last"}
		}},
		{"before reference", func(dir string) []string {
			return []string{"opik-export", "--out", dir, "last"}
		}},
		{"after reference", func(dir string) []string {
			return []string{"opik-export", "last", "--out=" + dir}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client, err := opik.New(opik.Config{Endpoint: "https://opik.invalid/api", Project: "review", Policy: sharingPolicy(), HTTPClient: &http.Client{Transport: exportTransport(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("unexpected network request")
			})}})
			require.NoError(t, err)
			selected := filepath.Join(t.TempDir(), "selected")
			fallback := filepath.Join(t.TempDir(), "fallback")
			var output, diagnostic bytes.Buffer
			code := evalcli.Main(t.Context(), tt.args(selected), evalcli.Config{StoreDir: fallback, Stdout: &output, Stderr: &diagnostic, Extensions: []evalcli.Extension{client.Extension()}})
			assert.Equal(t, 1, code, diagnostic.String())
			assert.Contains(t, diagnostic.String(), "last")
			assert.Zero(t, requests)
			_, err = os.Stat(filepath.Join(selected, ".gitignore"))
			require.NoError(t, err)
			_, err = os.Stat(fallback)
			assert.True(t, os.IsNotExist(err), "fallback store should remain unopened: %v", err)
		})
	}
}
