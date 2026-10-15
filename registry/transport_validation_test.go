package registry_test

import (
	"context"
	"encoding/json/v2"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type registryInvalidField struct {
	path  string
	name  string
	value any
}

func TestRegistryTransportValidatesSuppliedFields(t *testing.T) {
	service := registrypb.File_loomgen_registry_registry_proto.Services().Get(0)
	for _, tc := range registryTransportCases() {
		t.Run(tc.name, func(t *testing.T) {
			valid, err := json.Marshal(tc.payload)
			require.NoError(t, err)
			_, err = tc.build(string(valid))
			require.NoError(t, err, "the control request must satisfy every constraint")
			var calls atomic.Int32
			conn := registryTransportConnection(t, tc, func(context.Context, any) (any, error) {
				calls.Add(1)
				return tc.result, nil
			})
			method := service.Methods().ByName(protoreflect.Name(tc.name))
			require.NotNil(t, method)
			for _, field := range registryInvalidFields() {
				// These lookup/retirement contracts declare only a minimum name length.
				if field.path == "name" && field.name == "too long" && (tc.name == "Unregister" || tc.name == "GetToolset") {
					continue
				}
				var document map[string]any
				require.NoError(t, json.Unmarshal(valid, &document))
				if !replaceRegistryField(document, field.path, field.value) {
					continue // The constraint applies only to operations that carry this field.
				}
				t.Run(field.path+"/"+field.name, func(t *testing.T) {
					encoded, err := json.Marshal(document)
					require.NoError(t, err)
					_, err = tc.build(string(encoded))
					require.Error(t, err, "CLI must reject a supplied field outside its contract")
					message := dynamicpb.NewMessage(method.Input())
					require.NoError(t, protojson.Unmarshal(encoded, message), "the invalid value must still be valid protobuf JSON")
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					err = conn.Invoke(ctx, "/"+string(service.FullName())+"/"+tc.name, message, dynamicpb.NewMessage(method.Output()))
					require.Error(t, err)
					assert.Equal(t, codes.InvalidArgument, status.Code(err))
					assert.Zero(t, calls.Load(), "invalid values must not reach service code")
				})
			}
		})
	}
}

// These are externally visible constraints from the registry contract, not
// values derived from the generated validator being tested.
func registryInvalidFields() []registryInvalidField {
	return []registryInvalidField{
		{"name", "empty", ""},
		{"name", "too long", strings.Repeat("a", 257)},
		{"toolset", "empty", ""},
		{"toolset", "too long", strings.Repeat("a", 257)},
		{"tool", "empty", ""},
		{"tool", "too long", strings.Repeat("a", 257)},
		{"query", "empty", ""},
		{"query", "too long", strings.Repeat("a", 1025)},
		{"description", "too long", strings.Repeat("a", 4097)},
		{"ping_id", "too long", strings.Repeat("a", 257)},
		{"meta.turn_id", "empty", ""},
		{"meta.parent_tool_call_id", "empty", ""},
		{"provider_id", "empty", ""},
		{"provider_id", "too long", strings.Repeat("a", 513)},
		{"provider_id", "NUL", "provider\x00other"},
		{"provider_incarnation_id", "not UUID", "not-a-uuid"},
		{"expected_registration_token", "not token", "bad"},
		{"registration_token", "not token", "bad"},
		{"call_registration_token", "not token", "bad"},
		{"provider_registration_token", "not token", "bad"},
		{"admission_revision", "bad syntax", "!revision"},
		{"version", "not semver", "not-a-version"},
		{"wire_protocol_version", "incompatible", 0},
		{"settlement_duration_ms", "zero", 0},
		{"settlement_duration_ms", "too long", 86400001},
		{"ping_id", "empty", ""},
		{"request_event_id", "not stream ID", "event"},
		{"tool_use_id", "empty", ""},
		{"tool_use_id", "NUL", "use\x00other"},
		{"tool_use_id", "too long", strings.Repeat("a", 257)},
		{"stream", "empty", ""},
		{"stream", "too long", strings.Repeat("a", 129)},
		{"stream", "NUL", "stdout\x00other"},
		{"delta", "empty", ""},
		{"delta", "too large", strings.Repeat("a", 65537)},
		{"meta", "absent", nil},
		{"meta.run_id", "empty", ""},
		{"meta.run_id", "too long", strings.Repeat("a", 257)},
		{"meta.run_id", "NUL", "run\x00other"},
		{"meta.session_id", "empty", ""},
		{"meta.session_id", "too long", strings.Repeat("a", 257)},
		{"meta.session_id", "NUL", "session\x00other"},
		{"meta.tool_call_id", "empty", ""},
		{"meta.tool_call_id", "too long", strings.Repeat("a", 257)},
		{"meta.tool_call_id", "NUL", "call\x00other"},
		{"meta.turn_id", "too long", strings.Repeat("a", 257)},
		{"meta.turn_id", "NUL", "turn\x00other"},
		{"meta.parent_tool_call_id", "too long", strings.Repeat("a", 257)},
		{"meta.parent_tool_call_id", "NUL", "parent\x00other"},
	}
}

// replaceRegistryField changes only an existing object field so one rule can
// cover the operations sharing a wire field without adding unknown fields.
func replaceRegistryField(document map[string]any, path string, value any) bool {
	parts := strings.Split(path, ".")
	object := document
	for _, part := range parts[:len(parts)-1] {
		next, ok := object[part].(map[string]any)
		if !ok {
			return false
		}
		object = next
	}
	key := parts[len(parts)-1]
	if _, ok := object[key]; !ok {
		return false
	}
	object[key] = value
	return true
}
