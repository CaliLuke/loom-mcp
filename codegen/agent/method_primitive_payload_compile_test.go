package codegen_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
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
				Service("assistant", func() {
					Method("search", func() {
						Payload(tc.shape)
						Result(tc.shape)
					})
					Agent("planner", "Planner", func() {
						Use("lookup", func() {
							Tool("search", "Search", func() {
								Args(tc.shape)
								Return(tc.shape)
								BindTo("assistant", "search")
							})
						})
					})
				})
			})
			dir := writeGeneratedModule(t, files)
			source := fmt.Sprintf(methodPrimitivePayloadRuntimeTest, tc.goType, tc.value, tc.payload)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "primitive_runtime_test.go"), []byte(source), 0o600))
			cmd := exec.CommandContext(t.Context(), "go", "test", "-short", "-race", "-p", "2", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
			output, err := cmd.CombinedOutput()
			require.NoErrorf(t, err, "generated %s payload must compile and round-trip:\n%s", tc.name, output)
		})
	}
}
