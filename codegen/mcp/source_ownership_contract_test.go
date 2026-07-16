package codegen

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPCodegenDoesNotRewriteRenderedSource(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(current)
	sourceFS := os.DirFS(root)
	banned := []string{
		"renderedSectionSource(",
		"section.Write(&",
		"strings.Replace(source",
		"strings.ReplaceAll(source",
		"regexp.MustCompile",
	}

	err := fs.WalkDir(sourceFS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := fs.ReadFile(sourceFS, path)
		if err != nil {
			return err
		}
		for _, token := range banned {
			require.NotContains(t, string(source), token, "%s must extend generators through owned sections", filepath.Join(root, path))
		}
		return nil
	})
	require.NoError(t, err)
}
