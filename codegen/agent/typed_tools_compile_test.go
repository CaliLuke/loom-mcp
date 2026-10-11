package codegen_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type descriptorDecl struct {
	name  string
	ident string
}

const descriptorRoundTripTest = `package %s

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertDescriptor[P, R any](t *testing.T, descriptor tools.TypedTool[P, R], ident tools.Ident, input []byte) {
	t.Helper()
	assert.Equal(t, ident, descriptor.Name)
	var named tools.Named = descriptor
	assert.Equal(t, ident, named.Ident())
	require.NotNil(t, descriptor.Result.ToJSON)
	require.NotNil(t, descriptor.Result.FromJSON)
	value, err := descriptor.Payload.FromJSON(input)
	require.NoError(t, err)
	encoded, err := descriptor.Payload.ToJSON(value)
	require.NoError(t, err)
	roundTrip, err := descriptor.Payload.FromJSON(encoded)
	require.NoError(t, err)
	assert.Equal(t, value, roundTrip)
}

func TestDescriptors(t *testing.T) {
%s
}
`

// TestGeneratedTypedToolsCompile proves every descriptor in each typed-codec
// golden design compiles with its exact codecs and round-trips its payload.
func TestGeneratedTypedToolsCompile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		design func()
	}{
		{"args_primitive", testscenarios.ArgsPrimitive()},
		{"tool_specs_minimal", testscenarios.ToolSpecsMinimal()},
		{"args_inline", testscenarios.ArgsInlineObject()},
		{"args_usertype", testscenarios.ArgsUserType()},
		{"args_union_sum_types", testscenarios.ArgsUnionSumTypes()},
		{"args_located_nested_usertype", testscenarios.ArgsLocatedNestedUserType()},
		{"deep_nested_validations", testscenarios.DeepNestedValidations()},
		{"imports_deterministic", testscenarios.ImportsDeterministic()},
		{"method_complex_embedded", testscenarios.MethodComplexEmbedded()},
		{"service_alias_consistency", testscenarios.ServiceToolsetBindSelf()},
		{"agenttools_helpers", testscenarios.ExportsSimple()},
		{"no_result", testscenarios.NoResultMethod()},
		{"descriptor_collision", descriptorCollisionDesign([]string{"search", "search_tool", "spec_lookup", "with_lookup", "inject_lookup", "decode_lookup", "lookup_tool"}, true)},
		{"descriptor_collision_spec", descriptorCollisionDesign([]string{"spec_lookup", "lookup_tool"}, false)},
		{"descriptor_collision_with", descriptorCollisionDesign([]string{"with_lookup", "lookup_tool"}, false)},
		{"descriptor_collision_injection", descriptorCollisionDesign([]string{"inject_lookup", "decode_lookup", "lookup_tool"}, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := generateCompileDesign(t, tc.design)
			moduleDir := writeGeneratedModule(t, files)
			packages := 0
			for _, file := range files {
				if filepath.Base(file.Path) != "codecs.go" {
					continue
				}
				content := testhelpers.FileContent(t, files, file.Path)
				pkg, descriptors := typedDescriptorDecls(t, content)
				specs := testhelpers.FileContent(t, files, filepath.Join(filepath.Dir(file.Path), "specs.go"))
				require.NotEmpty(t, descriptors)
				assert.Len(t, descriptors, strings.Count(specs, "tools.Ident ="), "one descriptor per tool")
				if tc.name == "args_primitive" {
					assert.Contains(t, content, "tools.TypedTool[*EchoPayload, EchoResult]")
				}
				if tc.name == "descriptor_collision" {
					assert.Contains(t, content, "SearchTool2 = tools.TypedTool[*SearchPayload, SearchResult]")
					assert.Contains(t, content, "SearchToolTool = tools.TypedTool[*SearchToolPayload, SearchToolResult]")
					assert.Contains(t, specs, `Search tools.Ident = "lookup.search"`)
					assert.Contains(t, specs, `SearchTool tools.Ident = "lookup.search_tool"`)
					for _, name := range []string{"SpecLookup", "WithLookup", "InjectLookup", "DecodeLookup"} {
						assert.Contains(t, content, name+"Tool2 = tools.TypedTool[*"+name+"Payload, "+name+"Result]")
						assert.Contains(t, specs, name+` tools.Ident = "lookup.`)
					}
				}
				calls := make([]string, 0, len(descriptors))
				for _, descriptor := range descriptors {
					input := "Spec" + descriptor.ident + ".Payload.ExampleJSON"
					if strings.HasPrefix(tc.name, "descriptor_collision") && descriptor.ident == "LookupTool" {
						input = `[]byte("{\"query\":\"query\",\"session_id\":\"session-a\"}")`
					}
					calls = append(calls, fmt.Sprintf("t.Run(%q, func(t *testing.T) { assertDescriptor(t, %s, %s, %s) })", descriptor.name, descriptor.name, descriptor.ident, input))
				}
				source := fmt.Sprintf(descriptorRoundTripTest, pkg, strings.Join(calls, "\n"))
				path := filepath.Join(moduleDir, filepath.Dir(file.Path), "typed_tools_test.go")
				require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
				packages++
			}
			require.Positive(t, packages)
			if tc.name == "agenttools_helpers" {
				source := `package search
import (
	"testing"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
)
func TestDescriptorReExport(t *testing.T) {
	var descriptor tools.TypedTool[*FindPayload, FindResult] = FindTool
	assert.Equal(t, Find, descriptor.Name)
}
`
				path := filepath.Join(moduleDir, "gen/alpha/agents/scribe/agenttools/search/typed_tools_test.go")
				require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
			}
			if tc.name == "descriptor_collision" {
				source := `package lookup
import (
	"testing"
	lookup "example.com/fmcp/gen/alpha/toolsets/lookup"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
func TestDescriptorReExports(t *testing.T) {
	var first tools.TypedTool[*SearchPayload, SearchResult] = SearchTool2
	var second tools.TypedTool[*SearchToolPayload, SearchToolResult] = SearchToolTool
	assert.Equal(t, Search, first.Name)
	assert.Equal(t, SearchTool, second.Name)
	for _, tc := range []struct {
		name string
		descriptor tools.Named
		ident tools.Ident
	}{
		{"spec", SpecLookupTool2, SpecLookup},
		{"executor", WithLookupTool2, WithLookup},
		{"inject", InjectLookupTool2, InjectLookup},
		{"decode", DecodeLookupTool2, DecodeLookup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.ident, tc.descriptor.Ident())
		})
	}
}
func TestInjectionNamesPreserved(t *testing.T) {
	meta := runtime.ToolCallMeta{SessionID: "session-a"}
	payload := &LookupToolPayload{Query: "query"}
	require.NoError(t, lookup.InjectLookupTool(payload, meta, nil))
	require.NotNil(t, payload.SessionID)
	assert.Equal(t, meta.SessionID, *payload.SessionID)
	decoded, err := lookup.DecodeLookupTool([]byte("{\"query\":\"query\",\"session_id\":\"original\"}"), meta, nil)
	require.NoError(t, err)
	require.NotNil(t, decoded.SessionID)
	assert.Equal(t, meta.SessionID, *decoded.SessionID)
}
`
				path := filepath.Join(moduleDir, "gen/alpha/agents/scribe/lookup/typed_tools_test.go")
				require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
			}
			cmd := exec.CommandContext(t.Context(), "go", "test", "-short", "-race", "./...")
			cmd.Dir = moduleDir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "generated descriptors must compile and round-trip:\n%s", output)
		})
	}
}

// typedDescriptorDecls reads the emitted contract without assuming descriptor
// names, which may have collision suffixes allocated by the generator scope.
func typedDescriptorDecls(t *testing.T, source string) (string, []descriptorDecl) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "codecs.go", source, 0)
	require.NoError(t, err)
	var descriptors []descriptorDecl
	ast.Inspect(file, func(node ast.Node) bool {
		decl, ok := node.(*ast.ValueSpec)
		if !ok || len(decl.Values) != 1 {
			return true
		}
		value, ok := decl.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		generic, ok := value.Type.(*ast.IndexListExpr)
		if !ok {
			return true
		}
		selector, ok := generic.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "TypedTool" {
			return true
		}
		for _, field := range value.Elts {
			pair, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok || key.Name != "Name" {
				continue
			}
			ident, ok := pair.Value.(*ast.Ident)
			require.True(t, ok)
			descriptors = append(descriptors, descriptorDecl{decl.Names[0].Name, ident.Name})
		}
		return true
	})
	return file.Name.Name, descriptors
}

// descriptorCollisionDesign covers constants and derived Spec, With, Inject,
// and Decode identifiers, including server injection and used-toolset aliases.
func descriptorCollisionDesign(names []string, injected bool) func() {
	return func() {
		API("typed-tools", func() {})
		Service("alpha", func() {
			Method("Search", func() {
				Payload(func() {
					Attribute("query", String)
					Attribute("session_id", String)
					Required("query", "session_id")
				})
				Result(String)
			})
			Agent("scribe", "Search", func() {
				Use("lookup", func() {
					for _, name := range names {
						Tool(name, "Search", func() {
							Args(func() {
								Attribute("query", String)
								Attribute("session_id", String)
								Required("query", "session_id")
							})
							Return(String)
							BindTo("alpha", "Search")
							if injected && name == "lookup_tool" {
								Inject("session_id")
							}
						})
					}
				})
			})
		})
	}
}
