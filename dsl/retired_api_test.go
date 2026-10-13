package dsl_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	runtime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetiredHistoryAPIAbsent(t *testing.T) {
	for _, tc := range []struct {
		dir   string
		names []string
	}{
		{".", []string{"History", "KeepRecentTurns", "CompressAtTurns", "CompressAtMaxInputTokens", "KeepMaxTurns", "KeepMaxInputTokens"}},
		{"../expr/agent", []string{"HistoryExpr", "HistoryMode", "HistoryModeKeepRecent", "HistoryModeCompress"}},
		{"../runtime/agent/runtime", []string{"HistoryPolicy", "KeepRecentTurns", "Compress", "HistoryCompressionConfig", "CompressOption", "WithSummaryPrompt", "WithSummaryRole", "WithModelClass", "WithTokenCounter", "AgentsWithHistoryPolicy"}},
		{"../agenttest", []string{"CaptureHistoryError"}},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			entries, err := os.ReadDir(tc.dir)
			require.NoError(t, err)
			names := make(map[string]bool)
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(tc.dir, entry.Name()), nil, 0)
				require.NoError(t, err)
				for _, decl := range file.Decls {
					switch d := decl.(type) {
					case *ast.FuncDecl:
						names[d.Name.Name] = true
					case *ast.GenDecl:
						for _, spec := range d.Specs {
							switch v := spec.(type) {
							case *ast.TypeSpec:
								names[v.Name.Name] = true
							case *ast.ValueSpec:
								for _, name := range v.Names {
									names[name.Name] = true
								}
							}
						}
					}
				}
			}
			for _, name := range tc.names {
				assert.False(t, names[name], "retired API %s still exists", name)
			}
		})
	}
	for _, policy := range []any{agentexpr.RunPolicyExpr{}, runtime.RunPolicy{}} {
		_, exists := reflect.TypeOf(policy).FieldByName("History")
		assert.False(t, exists, "retired History field in %T", policy)
	}
}
