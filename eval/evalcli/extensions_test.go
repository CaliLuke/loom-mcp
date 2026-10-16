package evalcli_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/stretchr/testify/assert"
)

func TestExplicitExtensionWithoutHarness(t *testing.T) {
	var got []string
	cfg := evalcli.Config{StoreDir: t.TempDir(), Extensions: []evalcli.Extension{{Name: "publish", Help: "publish help", Run: func(ctx context.Context, request evalcli.ExtensionRequest) error {
		got = request.Args
		_, err := fmt.Fprintln(request.Stdout, "exported")
		return err
	}}}}
	code, out, diagnostic := invoke(t.Context(), cfg, "publish", "experiment")
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"experiment"}, got)
	assert.Equal(t, "exported\n", out)
	assert.Empty(t, diagnostic)
	code, out, diagnostic = invoke(t.Context(), cfg, "help", "publish")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "publish help\n")
	assert.Contains(t, out, "--out")
	assert.Empty(t, diagnostic)
}

func TestExtensionRegistrationAndOperationalErrors(t *testing.T) {
	tests := []struct {
		name       string
		extensions []evalcli.Extension
		code       int
	}{
		{"not enabled", nil, 2},
		{"shadowed", []evalcli.Extension{{Name: "run", Run: extensionFailure}}, 2},
		{"operational", []evalcli.Extension{{Name: "publish", Run: extensionFailure}}, 1},
		{"duplicate", []evalcli.Extension{{Name: "publish", Run: extensionFailure}, {Name: "publish", Run: extensionFailure}}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, diagnostic := invoke(t.Context(), evalcli.Config{StoreDir: t.TempDir(), Extensions: tt.extensions}, "publish", "experiment")
			assert.Equal(t, tt.code, code)
			assert.NotEmpty(t, diagnostic)
		})
	}
}

func extensionFailure(context.Context, evalcli.ExtensionRequest) error {
	return errors.New("export failed")
}
