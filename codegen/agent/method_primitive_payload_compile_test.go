package codegen_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	codegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

const methodPrimitivePayloadRuntimeTest = `package fmcp_test

import (
	"context"
	"encoding/json/v2"
	"testing"

	assistant "example.com/fmcp/gen/assistant"
	lookup "example.com/fmcp/gen/assistant/toolsets/lookup"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type payload = %[1]s

type echoService struct {
	seen payload
	calls int
}

var _ assistant.Service = (*echoService)(nil)

func (s *echoService) Search(_ context.Context, value payload) (payload, error) {
	s.seen = value
	s.calls++
	return value, nil
}

func TestDispatcherRoundTrip(t *testing.T) {
	svc := &echoService{}
	var want payload = %[2]s
	result, err := lookup.DispatchSearchMethod(t.Context(), &runtime.ToolCallMeta{}, []byte(%[3]q), nil, lookup.SearchDispatchOptions{
		Call: func(ctx context.Context, value any) (any, error) {
			return svc.Search(ctx, value.(payload))
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.Error)
	assert.Equal(t, 1, svc.calls)
	assert.Equal(t, want, svc.seen)
	encoded, err := lookup.SearchResultCodec.ToJSON(result.Result.(lookup.SearchResult))
	require.NoError(t, err)
	var got payload
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, want, got)
}

func TestProviderRoundTrip(t *testing.T) {
	svc := &echoService{}
	var want payload = %[2]s
	provider := lookup.NewProvider(svc)
	ctx := toolregistry.WithToolUseID(t.Context(), "use-1")
	result, err := provider.HandleToolCall(ctx, toolregistry.ToolCallMessage{
		RegistrationToken: "registration-token",
		ToolUseID: "use-1",
		Tool: lookup.Search,
		Payload: []byte(%[3]q),
		Meta: &toolregistry.ToolCallMeta{},
	})
	require.NoError(t, err)
	require.Nil(t, result.Error)
	assert.Equal(t, 1, svc.calls)
	assert.Equal(t, want, svc.seen)
	var got payload
	require.NoError(t, json.Unmarshal(result.Result, &got))
	assert.Equal(t, want, got)
}
`

func TestMethodPrimitivePayloadsCompileAndRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		shape   any
		goType  string
		value   string
		payload string
	}{
		{"any", Any, "loom.JSONValue", "loom.JSONValue(`{\"query\":\"search\"}`)", `{"query":"search"}`},
		{"object", "object", "*assistant.Item", `&assistant.Item{Query: "search"}`, `{"query":"search"}`},
		{"object_array", "object_array", "[]*assistant.Item", `[]*assistant.Item{{Query: "search"}}`, `[{"query":"search"}]`},
		{"object_map", "object_map", "map[string]*assistant.Item", `map[string]*assistant.Item{"first": {Query: "search"}}`, `{"first":{"query":"search"}}`},
		{"string", String, "string", `"search query"`, `"search query"`},
		{"boolean", Boolean, "bool", "true", "true"},
		{"int", Int, "int", "42", "42"},
		{"int32", Int32, "int32", "42", "42"},
		{"int64", Int64, "int64", "9007199254740993", "9007199254740993"},
		{"uint", UInt, "uint", "42", "42"},
		{"uint32", UInt32, "uint32", "42", "42"},
		{"uint64", UInt64, "uint64", "18446744073709551615", "18446744073709551615"},
		{"float32", Float32, "float32", "1.25", "1.25"},
		{"float64", Float64, "float64", "1.25", "1.25"},
		{"bytes", Bytes, "[]byte", `[]byte("query")`, `"cXVlcnk="`},
		{"array", ArrayOf(String), "[]string", `[]string{"first", "second"}`, `["first","second"]`},
		{"map", MapOf(String, Int), "map[string]int", `map[string]int{"first": 1, "second": 2}`, `{"first":1,"second":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := generateCompileDesign(t, func() {
				API("assistant", func() {})
				shape := tc.shape
				if name, ok := shape.(string); ok {
					item := Type("Item", func() {
						Attribute("query", String)
						Required("query")
					})
					switch name {
					case "object":
						shape = item
					case "object_array":
						shape = ArrayOf(item)
					case "object_map":
						shape = MapOf(String, item)
					}
				}
				Service("assistant", func() {
					Method("search", func() {
						Payload(shape)
						Result(shape)
					})
					Agent("planner", "Planner", func() {
						Use("lookup", func() {
							Tool("search", "Search", func() {
								Args(shape)
								Return(shape)
								BindTo("assistant", "search")
							})
						})
					})
				})
			})
			examples, err := codegen.GenerateExample("example.com/fmcp/gen", []eval.Root{goaexpr.Root, agentsexpr.Root}, nil)
			require.NoError(t, err)
			executor := testhelpers.FileContent(t, examples, "internal/agents/assistant/planner/toolsets/lookup/execute.go")
			// Compile the documented transform as executable code alongside the round trips.
			var transform string
			for line := range strings.SplitSeq(executor, "\n") {
				if strings.Contains(line, "// mp :=") {
					transform = strings.TrimSpace(strings.Replace(line, "// mp :=", "methodIn :=", 1))
				}
			}
			require.NotEmpty(t, transform)
			dir := writeGeneratedModule(t, files)
			source := fmt.Sprintf(methodPrimitivePayloadRuntimeTest, tc.goType, tc.value, tc.payload)
			if tc.name == "object" {
				source = strings.ReplaceAll(source, "result.Result.(lookup.SearchResult)", "result.Result.(*lookup.SearchResult)")
			}
			if tc.name == "any" {
				source = strings.Replace(source, `"testing"`, `"testing"; loom "github.com/CaliLuke/loom/pkg"`, 1)
			}
			transform = strings.ReplaceAll(transform, "lookupspecs.", "lookup.")
			source += fmt.Sprintf(`
func TestExampleTransform(t *testing.T) {
    args, err := lookup.UnmarshalSearchPayload([]byte(%q))
    require.NoError(t, err)
    %s
    var got payload = methodIn
    var want payload = %s
    assert.Equal(t, want, got)
}
`, tc.payload, transform, tc.value)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "primitive_runtime_test.go"), []byte(source), 0o600))
			// Generation uses global DSL state; only compile fully written, isolated modules in parallel.
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), "go", "test", "-short", "-race", "-count=1", "-p", "2", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			output, err := cmd.CombinedOutput()
			require.NoErrorf(t, err, "generated %s payload must compile and round-trip:\n%s", tc.name, output)
		})
	}
}
