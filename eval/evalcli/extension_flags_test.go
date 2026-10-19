package evalcli_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionOutPlacementAndArgumentOwnership(t *testing.T) {
	tests := []struct {
		name string
		args func(string) []string
		want []string
	}{
		{"before command", func(dir string) []string {
			return []string{"--out", dir, "publish", "last"}
		}, []string{"last"}},
		{"before positional", func(dir string) []string {
			return []string{"publish", "--out", dir, "last"}
		}, []string{"last"}},
		{"after positional", func(dir string) []string {
			return []string{"publish", "last", "--out", dir}
		}, []string{"last"}},
		{"equals", func(dir string) []string {
			return []string{"publish", "last", "--out=" + dir}
		}, []string{"last"}},
		{"same repeated", func(dir string) []string {
			return []string{"--out", dir, "publish", "last", "--out=" + dir}
		}, []string{"last"}},
		{"legacy short prefix", func(dir string) []string {
			return []string{"-out", dir, "publish", "last"}
		}, []string{"last"}},
		{"legacy json prefix", func(dir string) []string {
			return []string{"--json", "publish", "last", "--out", dir}
		}, []string{"last"}},
		{"unknown flags", func(dir string) []string {
			return []string{"publish", "--opaque", "value", "last", "--out", dir, "--more=x"}
		}, []string{"--opaque", "value", "last", "--more=x"}},
		{"delimiter", func(dir string) []string {
			return []string{"publish", "--out", dir, "--", "last", "--out", "literal"}
		}, []string{"last", "--out", "literal"}},
		{"prefix delimiter", func(dir string) []string {
			return []string{"--out", dir, "--", "publish", "last", "--out", "literal"}
		}, []string{"last", "--out", "literal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected := filepath.Join(t.TempDir(), "selected")
			fallback := filepath.Join(t.TempDir(), "fallback")
			var got []string
			cfg := evalcli.Config{StoreDir: fallback, Extensions: []evalcli.Extension{{Name: "publish", Run: func(_ context.Context, request evalcli.ExtensionRequest) error {
				got = request.Args
				assert.Equal(t, selected, request.Store.Dir())
				return nil
			}}}}
			code, _, diagnostic := invoke(t.Context(), cfg, tt.args(selected)...)
			assert.Equal(t, 0, code, diagnostic)
			assert.Equal(t, tt.want, got)
			_, err := os.Stat(filepath.Join(selected, ".gitignore"))
			require.NoError(t, err)
			_, err = os.Stat(fallback)
			assert.True(t, os.IsNotExist(err), "fallback store should remain unopened: %v", err)
		})
	}
}

func TestExtensionOutRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		args func(string, string) []string
	}{
		{"missing", func(_, _ string) []string {
			return []string{"publish", "last", "--out"}
		}},
		{"empty", func(_, _ string) []string {
			return []string{"publish", "last", "--out="}
		}},
		{"conflicting suffix", func(a, b string) []string {
			return []string{"publish", "last", "--out", a, "--out", b}
		}},
		{"conflicting prefix suffix", func(a, b string) []string {
			return []string{"--out", a, "publish", "last", "--out", b}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := filepath.Join(t.TempDir(), "first")
			second := filepath.Join(t.TempDir(), "second")
			called := false
			cfg := evalcli.Config{StoreDir: filepath.Join(t.TempDir(), "fallback"), Extensions: []evalcli.Extension{{Name: "publish", Run: func(context.Context, evalcli.ExtensionRequest) error {
				called = true
				return nil
			}}}}
			code, _, diagnostic := invoke(t.Context(), cfg, tt.args(first, second)...)
			assert.Equal(t, 2, code)
			assert.NotEmpty(t, diagnostic)
			assert.False(t, called)
		})
	}
}

func TestExtensionHelpExplainsOutOwnership(t *testing.T) {
	cfg := evalcli.Config{Extensions: []evalcli.Extension{{Name: "publish", Help: "publish <ref>", Run: extensionFailure}}}
	code, out, diagnostic := invoke(t.Context(), cfg, "help", "publish")
	assert.Equal(t, 0, code, diagnostic)
	assert.Contains(t, out, "publish <ref>")
	assert.Contains(t, out, "--out")
	assert.Contains(t, out, "--")
}

func TestInvalidExtensionRegistrationPrecedesBuiltInHelp(t *testing.T) {
	cfg := evalcli.Config{Extensions: []evalcli.Extension{{Name: "run", Run: extensionFailure}}}
	code, _, diagnostic := invoke(t.Context(), cfg, "help")
	assert.Equal(t, 2, code)
	assert.Contains(t, diagnostic, "shadows built-in")
}
