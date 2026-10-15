package registry_test

import (
	"context"
	"sync/atomic"
	"testing"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	loomgrpc "github.com/CaliLuke/loom/grpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Custom transport clients can omit the request object. The generated remote
// adapter must still apply server validation, while list-all accepts no filter.
func TestRegistryRemoteAdaptersValidateOmittedRequests(t *testing.T) {
	builders := map[string]func(registrypb.RegistryClient, ...grpc.CallOption) loomgrpc.RemoteFunc{
		"Register":               grpcclient.BuildRegisterFunc,
		"ReleaseProvider":        grpcclient.BuildReleaseProviderFunc,
		"DrainProvider":          grpcclient.BuildDrainProviderFunc,
		"Unregister":             grpcclient.BuildUnregisterFunc,
		"Pong":                   grpcclient.BuildPongFunc,
		"ListToolsets":           grpcclient.BuildListToolsetsFunc,
		"GetToolset":             grpcclient.BuildGetToolsetFunc,
		"Search":                 grpcclient.BuildSearchFunc,
		"CallTool":               grpcclient.BuildCallToolFunc,
		"RetryTool":              grpcclient.BuildRetryToolFunc,
		"CompleteToolCall":       grpcclient.BuildCompleteToolCallFunc,
		"PublishToolOutputDelta": grpcclient.BuildPublishToolOutputDeltaFunc,
		"ReportToolCallOverload": grpcclient.BuildReportToolCallOverloadFunc,
		"ClaimToolCall":          grpcclient.BuildClaimToolCallFunc,
	}
	for _, tc := range registryTransportCases() {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			conn := registryTransportConnection(t, tc, func(_ context.Context, payload any) (any, error) {
				calls.Add(1)
				assert.Equal(t, &genregistry.ListToolsetsPayload{}, payload)
				return tc.result, nil
			})
			remote := builders[tc.name](registrypb.NewRegistryClient(conn))
			response, err := remote(t.Context(), nil)
			if tc.name == "ListToolsets" {
				require.NoError(t, err)
				require.NotNil(t, response)
				assert.EqualValues(t, 1, calls.Load())
			} else {
				require.Error(t, err)
				assert.Equal(t, codes.InvalidArgument, status.Code(err))
				assert.Zero(t, calls.Load())
			}
		})
	}
}
