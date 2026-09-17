package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeneratedAnyToolTypesCompile covers imports in both public and transport
// definitions, including the no-runtime-import control.
func TestGeneratedAnyToolTypesCompile(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fieldType     func() goaexpr.DataType
		wantJSONValue bool
	}{
		{"direct", func() goaexpr.DataType {
			return Any
		}, true},
		{"array", func() goaexpr.DataType {
			return ArrayOf(Any)
		}, true},
		{"map", func() goaexpr.DataType {
			return MapOf(String, Any)
		}, true},
		{"string", func() goaexpr.DataType {
			return String
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := generateCompileDesign(t, func() {
				API("any-types", func() {
				})
				value := Type("Value", func() {
					Attribute("value", tc.fieldType())
				})
				Service("example", func() {
					Agent("assistant", "Assistant", func() {
						Use("query", func() {
							Tool("read", "Read", func() {
								Args(value)
								Return(value)
							})
						})
					})
				})
			})
			moduleDir := writeGeneratedModule(t, files)
			for _, path := range []string{"types.go", "http/types.go"} {
				content, err := os.ReadFile(filepath.Join(moduleDir, "gen/example/toolsets/query", path)) // #nosec G304 -- test-owned temp directory and fixed relative paths.
				require.NoError(t, err)
				if tc.wantJSONValue {
					assert.Contains(t, string(content), "loom.JSONValue", path)
					assert.Contains(t, string(content), `"github.com/CaliLuke/loom/pkg"`, path)
				} else {
					assert.NotContains(t, string(content), `"github.com/CaliLuke/loom/pkg"`, path)
				}
			}
			build := exec.CommandContext(t.Context(), "go", "test", "./...")
			build.Dir = moduleDir
			build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			out, err := build.CombinedOutput()
			require.NoErrorf(t, err, "generated Any types do not compile:\n%s", out)
		})
	}
}
