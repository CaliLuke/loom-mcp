package codegen_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

const collectionCodecRuntimeTest = `package fmcp_test

import (
	"testing"

	query "example.com/fmcp/gen/example/toolsets/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectionRoundTrip(t *testing.T) {
	input := []byte(%q)
	payload, err := query.ReadPayloadCodec.FromJSON(input)
	require.NoError(t, err)
	encodedPayload, err := query.ReadPayloadCodec.ToJSON(payload)
	require.NoError(t, err)
	assert.JSONEq(t, string(input), string(encodedPayload))

	result, err := query.ReadResultCodec.FromJSON(input)
	require.NoError(t, err)
	encodedResult, err := query.ReadResultCodec.ToJSON(result)
	require.NoError(t, err)
	assert.JSONEq(t, string(input), string(encodedResult))
}
`

// TestGeneratedCollectionCodecsCompile exercises both codec directions across
// the transport package boundary for local and explicitly located object types.
func TestGeneratedCollectionCodecsCompile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		located bool
		isMap   bool
		input   string
	}{
		{"local array", false, false, `{"items":[{"name":"first"}]}`},
		{"local map", false, true, `{"items":{"one":{"name":"first"}}}`},
		{"located array", true, false, `{"items":[{"name":"first"}]}`},
		{"located map", true, true, `{"items":{"one":{"name":"first"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := generateCompileDesign(t, func() {
				API("collection-codecs", func() {
				})
				item := Type("Item", func() {
					Attribute("name", String)
					Required("name")
					if tc.located {
						Meta("struct:pkg:path", "types")
					}
				})
				value := Type("Collection", func() {
					var collection goaexpr.DataType = ArrayOf(item)
					if tc.isMap {
						collection = MapOf(String, item)
					}
					Attribute("items", collection)
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
			source := fmt.Sprintf(collectionCodecRuntimeTest, tc.input)
			err := os.WriteFile(filepath.Join(moduleDir, "collection_runtime_test.go"), []byte(source), 0o600)
			require.NoError(t, err)
			build := exec.CommandContext(t.Context(), "go", "test", "./...")
			build.Dir = moduleDir
			build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			out, err := build.CombinedOutput()
			require.NoErrorf(t, err, "generated collection codecs failed:\n%s", out)
		})
	}
}
