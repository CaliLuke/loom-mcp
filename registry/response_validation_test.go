package registry_test

import (
	"context"
	"encoding/json/v2"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestRegistryClientRejectsWrongPayloadTypes(t *testing.T) {
	for _, tc := range registryTransportCases() {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			conn := registryTransportConnection(t, tc, func(context.Context, any) (any, error) {
				calls.Add(1)
				return tc.result, nil
			})
			_, err := tc.client(grpcclient.NewClient(conn))(t.Context(), struct{}{})
			require.ErrorContains(t, err, "invalid value expected")
			assert.Zero(t, calls.Load())
		})
	}
}

// A remote service can violate the schema even when local service code is
// correct. Exercise the client against raw wire replies, bypassing server-side
// response validation, and require rejection before constructing domain values.
func TestRegistryClientRejectsInvalidWireResponses(t *testing.T) {
	service := registrypb.File_loomgen_registry_registry_proto.Services().Get(0)
	for _, tc := range registryTransportCases() {
		if tc.result == nil {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			method := service.Methods().ByName(protoreflect.Name(tc.name))
			valid, err := json.Marshal(tc.result)
			require.NoError(t, err)
			for _, field := range registryInvalidResponseFields() {
				var doc map[string]any
				require.NoError(t, json.Unmarshal(valid, &doc))
				if !replaceResponseField(doc, strings.Split(field.path, "."), field.value) {
					continue
				}
				t.Run(field.path+"/"+field.name, func(t *testing.T) {
					raw, err := json.Marshal(doc)
					require.NoError(t, err)
					reply := dynamicpb.NewMessage(method.Output())
					require.NoError(t, protojson.Unmarshal(raw, reply))
					server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
						if err := stream.RecvMsg(dynamicpb.NewMessage(method.Input())); err != nil {
							return err
						}
						return stream.SendMsg(reply)
					}))
					conn := rawRegistryConnection(t, server)
					result, err := tc.client(grpcclient.NewClient(conn))(t.Context(), tc.payload)
					require.Error(t, err)
					assert.Nil(t, result)
				})
			}
		})
	}
}

func registryInvalidResponseFields() []registryInvalidField {
	return []registryInvalidField{
		{"registered_at", "absent", nil}, {"registered_at", "bad date", "invalid"},
		{"registration_token", "absent", nil}, {"registration_token", "bad token", "bad"},
		{"lease_duration_ms", "absent", nil}, {"lease_duration_ms", "zero", 0}, {"lease_duration_ms", "too long", 86400001},
		{"tool_use_id", "absent", nil}, {"tool_use_id", "empty", ""}, {"tool_use_id", "too long", strings.Repeat("a", 257)},
		{"execution_deadline", "absent", nil}, {"execution_deadline", "bad date", "invalid"},
		{"result_stream_expires_at", "absent", nil}, {"result_stream_expires_at", "bad date", "invalid"},
		{"disposition", "absent", nil}, {"disposition", "unknown", "invented"},
		{"name", "absent", nil}, {"name", "empty", ""}, {"name", "too long", strings.Repeat("a", 257)},
		{"version", "bad semver", "invalid"},
		{"tools.0.name", "absent", nil}, {"tools.0.name", "empty", ""}, {"tools.0.name", "too long", strings.Repeat("a", 257)},
		{"tools.0.payload_schema", "absent", nil}, {"tools.0.result_schema", "absent", nil},
		{"toolsets.0.name", "absent", nil}, {"toolsets.0.name", "empty", ""}, {"toolsets.0.name", "too long", strings.Repeat("a", 257)},
		{"toolsets.0.version", "bad semver", "invalid"},
		{"toolsets.0.registered_at", "absent", nil}, {"toolsets.0.registered_at", "bad date", "invalid"},
		{"toolsets.0.tool_count", "absent", nil},
	}
}

func replaceResponseField(value any, path []string, replacement any) bool {
	if len(path) == 0 {
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		child, ok := value[path[0]]
		if !ok {
			return false
		}
		if len(path) == 1 {
			value[path[0]] = replacement
			return true
		}
		return replaceResponseField(child, path[1:], replacement)
	case []any:
		index, err := strconv.Atoi(path[0])
		if err != nil || index < 0 || index >= len(value) {
			return false
		}
		return replaceResponseField(value[index], path[1:], replacement)
	default:
		return false
	}
}

func rawRegistryConnection(t *testing.T, server *grpc.Server) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()
	conn, err := grpc.NewClient("passthrough:///registry-wire", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, conn.Close())
		server.Stop()
		assert.NoError(t, <-done)
	})
	return conn
}
