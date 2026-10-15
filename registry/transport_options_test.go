package registry_test

import (
	"context"
	"sync/atomic"
	"testing"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestRegistryClientCallOptionsCaptureServerHeaders(t *testing.T) {
	for _, tc := range registryTransportCases() {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			conn := registryTransportConnection(t, tc, func(ctx context.Context, payload any) (any, error) {
				calls.Add(1)
				assert.Equal(t, tc.payload, payload)
				if err := grpc.SendHeader(ctx, metadata.Pairs("registry-revision", "revision-7")); err != nil {
					return nil, err
				}
				return tc.result, nil
			})
			var header metadata.MD
			result, err := tc.client(grpcclient.NewClient(conn, grpc.Header(&header)))(t.Context(), tc.payload)
			require.NoError(t, err)
			assert.Equal(t, []string{"revision-7"}, header.Get("registry-revision"))
			assert.EqualValues(t, 1, calls.Load())
			if tc.result != nil {
				assert.Equal(t, tc.result, result)
			}
		})
	}
}
