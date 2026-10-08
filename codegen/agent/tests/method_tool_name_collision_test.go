package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	gcodegen "github.com/CaliLuke/loom/codegen"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestGoifyCollidingMethodToolNamesCompileAndMapTheirShapes(t *testing.T) {
	files := buildAndGenerate(t, func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Method("GetFirst", func() {
				Payload(func() {
					Attribute("record", String)
					Required("record")
				})
				Result(func() {
					Attribute("one", String)
					Attribute("trace", String)
					Required("one", "trace")
				})
			})
			Method("GetSecond", func() {
				Payload(func() {
					Attribute("key", String)
					Required("key")
				})
				Result(func() {
					Attribute("two", String)
					Attribute("audit", Int)
					Required("two", "audit")
				})
			})
			Agent("scribe", "", func() {
				Use("lookup", func() {
					Tool("get_x", "", func() {
						Args(func() {
							Attribute("record", String)
							Required("record")
						})
						Return(func() {
							Attribute("one", String)
							Required("one")
						})
						BindTo("alpha", "GetFirst")
						ServerData("first", String, func() {
							FromMethodResultField("trace")
						})
					})
					Tool("getX", "", func() {
						Args(func() {
							Attribute("key", String)
							Required("key")
						})
						Return(func() {
							Attribute("two", String)
							Required("two")
						})
						BindTo("alpha", "GetSecond")
						ServerData("second", Int, func() {
							FromMethodResultField("audit")
						})
					})
				})
			})
		})
	})

	compileGeneratedMethodTransforms(t, files, `package lookup

import (
	"testing"
	alpha "github.com/CaliLuke/loom-mcp/v2/gen/alpha"
)

func TestCollidingToolTransformsKeepTheirBindings(t *testing.T) {
	firstPayload := InitGetXMethodPayload(&GetXPayload{Key: "first"})
	if firstPayload.Key != "first" {
		t.Fatalf("first payload mapped to %#v", firstPayload)
	}
	secondPayload := InitGetX2MethodPayload(&GetX2Payload{Record: "second"})
	if secondPayload.Record != "second" {
		t.Fatalf("second payload mapped to %#v", secondPayload)
	}
	firstResult := InitGetXToolResult(&alpha.GetSecondResult{Two: "first result"})
	if firstResult.Two != "first result" {
		t.Fatalf("first result mapped to %#v", firstResult)
	}
	secondResult := InitGetX2ToolResult(&alpha.GetFirstResult{One: "second result"})
	if secondResult.One != "second result" {
		t.Fatalf("second result mapped to %#v", secondResult)
	}
	if serverData := InitGetX2FirstServerData("first sidecar"); serverData != "first sidecar" {
		t.Fatalf("first server data mapped to %#v", serverData)
	}
	if serverData := InitGetXSecondServerData(27); serverData != 27 {
		t.Fatalf("second server data mapped to %#v", serverData)
	}
}
`)
}

func compileGeneratedMethodTransforms(t *testing.T, files []*gcodegen.File, generatedTest string) {
	t.Helper()
	dir := t.TempDir()
	module := `module github.com/CaliLuke/loom-mcp/v2

go 1.27.0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600))
	writeGeneratedFile(t, dir, "gen/alpha/types.go", `package alpha

type GetFirstPayload struct{ Record string }
type GetSecondPayload struct{ Key string }
type GetFirstResult struct{ One, Trace string }
type GetSecondResult struct {
	Two   string
	Audit int
}
type GetXFirstServerData = string
type GetXSecondServerData = int
`)
	for _, path := range []string{
		"gen/alpha/toolsets/lookup/types.go",
		"gen/alpha/toolsets/lookup/transforms.go",
	} {
		source := fileContent(t, files, path)
		require.NotEmpty(t, source)
		writeGeneratedFile(t, dir, path, source)
	}
	writeGeneratedFile(t, dir, "gen/alpha/toolsets/lookup/method_transforms_test.go", generatedTest)
	cmd := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "./...")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "generated method transforms must compile and map both tool shapes:\n%s", output)
}

func writeGeneratedFile(t *testing.T, root, path, source string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0700))
	require.NoError(t, os.WriteFile(fullPath, []byte(source), 0600))
}
