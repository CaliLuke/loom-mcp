package registry_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	grpcserver "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/server"
	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type registryTransportCase struct {
	name    string
	command string
	payload any
	result  any
	build   func(string) (any, error)
	bind    func(*genregistry.Endpoints, loom.Endpoint)
	client  func(*grpcclient.Client) loom.Endpoint
	errors  []string
}

func TestRegistryTransportRoundTrip(t *testing.T) {
	for _, tc := range registryTransportCases() {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.payload)
			require.NoError(t, err)
			payload, err := tc.build(string(body))
			require.NoError(t, err)
			assert.Equal(t, tc.payload, payload, "CLI input must preserve the authored payload")
			var calls atomic.Int32
			conn := registryTransportConnection(t, tc, func(_ context.Context, actual any) (any, error) {
				calls.Add(1)
				assert.Equal(t, tc.payload, actual, "wire conversion must preserve all request fields")
				return tc.result, nil
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := tc.client(grpcclient.NewClient(conn))(ctx, payload)
			require.NoError(t, err)
			assert.EqualValues(t, 1, calls.Load())
			if tc.result != nil {
				assert.Equal(t, tc.result, result, "wire conversion must preserve all response fields")
			}
		})
	}
}

func TestRegistryTransportDeclaredErrors(t *testing.T) {
	for _, tc := range registryTransportCases() {
		for _, name := range tc.errors {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				conn := registryTransportConnection(t, tc, func(context.Context, any) (any, error) {
					return nil, loom.PermanentError(name, "public repair detail")
				})
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				_, err := tc.client(grpcclient.NewClient(conn))(ctx, tc.payload)
				var serviceError *loom.ServiceError
				require.ErrorAs(t, err, &serviceError)
				assert.Equal(t, name, serviceError.Name)
				assert.Contains(t, serviceError.Message, "public repair detail")
			})
		}
	}
}

func TestRegistryTransportRedactsPrivateErrors(t *testing.T) {
	for _, tc := range registryTransportCases() {
		t.Run(tc.name, func(t *testing.T) {
			conn := registryTransportConnection(t, tc, func(context.Context, any) (any, error) {
				return nil, errors.New("private registry storage diagnostic")
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err := tc.client(grpcclient.NewClient(conn))(ctx, tc.payload)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private registry storage diagnostic")
		})
	}
}

func TestRegistryTransportRejectsAbsentRequiredFields(t *testing.T) {
	service := registrypb.File_loomgen_registry_registry_proto.Services().Get(0)
	for _, tc := range registryTransportCases() {
		if tc.name == "ListToolsets" {
			continue // An empty filter is the documented list-all request.
		}
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			conn := registryTransportConnection(t, tc, func(context.Context, any) (any, error) {
				calls.Add(1)
				return tc.result, nil
			})
			method := service.Methods().ByName(protoreflect.Name(tc.name))
			require.NotNil(t, method)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := conn.Invoke(ctx, "/"+string(service.FullName())+"/"+tc.name,
				dynamicpb.NewMessage(method.Input()), dynamicpb.NewMessage(method.Output()))
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Zero(t, calls.Load(), "invalid transport input must not reach the service")
		})
	}
}

func TestRegistryCLIPayloadRejectsMalformedJSON(t *testing.T) {
	for _, tc := range registryTransportCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.build("{")
			require.Error(t, err)
			if tc.name != "ListToolsets" {
				_, err = tc.build("{}")
				require.Error(t, err, "CLI must reject missing required request fields")
			}
		})
	}
}

// registryTransportConnection exercises generated adapters through an actual
// gRPC connection without Redis, timers, or a separately deployed service.
func registryTransportConnection(t *testing.T, tc registryTransportCase, endpoint loom.Endpoint, options ...grpc.ServerOption) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(options...)
	endpoints := &genregistry.Endpoints{}
	tc.bind(endpoints, endpoint)
	// Include the service/client endpoint adapters in the real transport path.
	service := genregistry.NewClient(endpoints.Register, endpoints.ReleaseProvider, endpoints.DrainProvider,
		endpoints.Unregister, endpoints.Pong, endpoints.ListToolsets, endpoints.GetToolset, endpoints.Search,
		endpoints.CallTool, endpoints.RetryTool, endpoints.CompleteToolCall, endpoints.PublishToolOutputDelta,
		endpoints.ReportToolCallOverload, endpoints.ClaimToolCall)
	endpoints = genregistry.NewEndpoints(service)
	registrypb.RegisterRegistryServer(server, grpcserver.New(endpoints, nil))
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()
	conn, err := grpc.NewClient("passthrough:///registry-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
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

func registryPayloadBuilder[T any](build func(string) (*T, error)) func(string) (any, error) {
	return func(raw string) (any, error) {
		return build(raw)
	}
}
