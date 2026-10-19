package eval_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

func TestStoreDirResolvesAtOpenAndSurvivesReopen(t *testing.T) {
	t.Chdir(t.TempDir())
	working, err := os.Getwd()
	require.NoError(t, err)
	want := filepath.Join(working, "experiments")
	for _, selected := range []string{"experiments", want} {
		t.Run(selected, func(t *testing.T) {
			store, err := eval.Open(selected)
			require.NoError(t, err)
			assert.Equal(t, want, store.Dir())

			t.Chdir(t.TempDir())
			assert.Equal(t, want, store.Dir())
			reopened, err := eval.Open(store.Dir())
			require.NoError(t, err)
			assert.Equal(t, want, reopened.Dir())
		})
	}
}
