package registry_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type registryActorKey struct{}

func TestRegistryMiddlewareGatesEveryRPC(t *testing.T) {
	for _, tc := range registryTransportCases() {
		for _, allow := range []bool{false, true} {
			name := tc.name + "/denied"
			if allow {
				name = tc.name + "/allowed"
			}
			t.Run(name, func(t *testing.T) {
				var middlewareCalls, endpointCalls atomic.Int32
				interceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
					middlewareCalls.Add(1)
					assert.True(t, strings.HasSuffix(info.FullMethod, "/"+tc.name))
					if !allow {
						return nil, status.Error(codes.PermissionDenied, "operator access denied")
					}
					return handler(context.WithValue(ctx, registryActorKey{}, "operator"), req)
				}
				conn := registryTransportConnection(t, tc, func(ctx context.Context, actual any) (any, error) {
					endpointCalls.Add(1)
					assert.Equal(t, "operator", ctx.Value(registryActorKey{}))
					assert.Equal(t, tc.payload, actual)
					return tc.result, nil
				}, grpc.UnaryInterceptor(interceptor))
				result, err := tc.client(grpcclient.NewClient(conn))(t.Context(), tc.payload)
				assert.EqualValues(t, 1, middlewareCalls.Load())
				if !allow {
					require.ErrorContains(t, err, "operator access denied")
					assert.Zero(t, endpointCalls.Load(), "rejected RPC must not execute service logic")
					return
				}
				require.NoError(t, err)
				assert.EqualValues(t, 1, endpointCalls.Load())
				if tc.result != nil {
					assert.Equal(t, tc.result, result)
				}
			})
		}
	}
}
