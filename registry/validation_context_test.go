package registry_test

import (
	"testing"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	grpcserver "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/server"
	"github.com/stretchr/testify/require"
)

// Applications may compose these public validators inside a larger request.
// The caller's path must survive both direct and nested validation failures.
func TestRegistryValidationPreservesCallerContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		validate func() error
	}{
		{"client/RegisterRequest", func() error {
			return grpcclient.ValidateRegisterRequest(&registrypb.RegisterRequest{}, "batch", "[2]")
		}},
		{"client/ToolSchema", func() error {
			return grpcclient.ValidateToolSchema(&registrypb.ToolSchema{}, "batch", "[2]")
		}},
		{"client/RegisterResponse", func() error {
			return grpcclient.ValidateRegisterResponse(&registrypb.RegisterResponse{}, "batch", "[2]")
		}},
		{"client/ReleaseProviderRequest", func() error {
			return grpcclient.ValidateReleaseProviderRequest(&registrypb.ReleaseProviderRequest{}, "batch", "[2]")
		}},
		{"client/DrainProviderRequest", func() error {
			return grpcclient.ValidateDrainProviderRequest(&registrypb.DrainProviderRequest{}, "batch", "[2]")
		}},
		{"client/UnregisterRequest", func() error {
			return grpcclient.ValidateUnregisterRequest(&registrypb.UnregisterRequest{}, "batch", "[2]")
		}},
		{"client/PongRequest", func() error {
			return grpcclient.ValidatePongRequest(&registrypb.PongRequest{}, "batch", "[2]")
		}},
		{"client/ListToolsetsResponse", func() error {
			return grpcclient.ValidateListToolsetsResponse(&registrypb.ListToolsetsResponse{Toolsets: []*registrypb.ToolsetInfo{nil}}, "batch", "[2]")
		}},
		{"client/ToolsetInfo", func() error {
			return grpcclient.ValidateToolsetInfo(&registrypb.ToolsetInfo{}, "batch", "[2]")
		}},
		{"client/GetToolsetRequest", func() error {
			return grpcclient.ValidateGetToolsetRequest(&registrypb.GetToolsetRequest{}, "batch", "[2]")
		}},
		{"client/GetToolsetResponse", func() error {
			return grpcclient.ValidateGetToolsetResponse(&registrypb.GetToolsetResponse{}, "batch", "[2]")
		}},
		{"client/SearchRequest", func() error {
			return grpcclient.ValidateSearchRequest(&registrypb.SearchRequest{}, "batch", "[2]")
		}},
		{"client/SearchResponse", func() error {
			return grpcclient.ValidateSearchResponse(&registrypb.SearchResponse{Toolsets: []*registrypb.ToolsetInfo{nil}}, "batch", "[2]")
		}},
		{"client/CallToolRequest", func() error {
			return grpcclient.ValidateCallToolRequest(&registrypb.CallToolRequest{}, "batch", "[2]")
		}},
		{"client/ToolCallMeta", func() error {
			return grpcclient.ValidateToolCallMeta(&registrypb.ToolCallMeta{}, "batch", "[2]")
		}},
		{"client/CallToolResponse", func() error {
			return grpcclient.ValidateCallToolResponse(&registrypb.CallToolResponse{}, "batch", "[2]")
		}},
		{"client/RetryToolRequest", func() error {
			return grpcclient.ValidateRetryToolRequest(&registrypb.RetryToolRequest{}, "batch", "[2]")
		}},
		{"client/RetryToolResponse", func() error {
			return grpcclient.ValidateRetryToolResponse(&registrypb.RetryToolResponse{}, "batch", "[2]")
		}},
		{"client/CompleteToolCallRequest", func() error {
			return grpcclient.ValidateCompleteToolCallRequest(&registrypb.CompleteToolCallRequest{}, "batch", "[2]")
		}},
		{"client/PublishToolOutputDeltaRequest", func() error {
			return grpcclient.ValidatePublishToolOutputDeltaRequest(&registrypb.PublishToolOutputDeltaRequest{}, "batch", "[2]")
		}},
		{"client/ReportToolCallOverloadRequest", func() error {
			return grpcclient.ValidateReportToolCallOverloadRequest(&registrypb.ReportToolCallOverloadRequest{}, "batch", "[2]")
		}},
		{"client/ClaimToolCallRequest", func() error {
			return grpcclient.ValidateClaimToolCallRequest(&registrypb.ClaimToolCallRequest{}, "batch", "[2]")
		}},
		{"client/ClaimToolCallResponse", func() error {
			return grpcclient.ValidateClaimToolCallResponse(&registrypb.ClaimToolCallResponse{}, "batch", "[2]")
		}},
		{"server/RegisterRequest", func() error {
			return grpcserver.ValidateRegisterRequest(&registrypb.RegisterRequest{}, "batch", "[2]")
		}},
		{"server/ToolSchema", func() error {
			return grpcserver.ValidateToolSchema(&registrypb.ToolSchema{}, "batch", "[2]")
		}},
		{"server/ReleaseProviderRequest", func() error {
			return grpcserver.ValidateReleaseProviderRequest(&registrypb.ReleaseProviderRequest{}, "batch", "[2]")
		}},
		{"server/DrainProviderRequest", func() error {
			return grpcserver.ValidateDrainProviderRequest(&registrypb.DrainProviderRequest{}, "batch", "[2]")
		}},
		{"server/UnregisterRequest", func() error {
			return grpcserver.ValidateUnregisterRequest(&registrypb.UnregisterRequest{}, "batch", "[2]")
		}},
		{"server/PongRequest", func() error {
			return grpcserver.ValidatePongRequest(&registrypb.PongRequest{}, "batch", "[2]")
		}},
		{"server/GetToolsetRequest", func() error {
			return grpcserver.ValidateGetToolsetRequest(&registrypb.GetToolsetRequest{}, "batch", "[2]")
		}},
		{"server/SearchRequest", func() error {
			return grpcserver.ValidateSearchRequest(&registrypb.SearchRequest{}, "batch", "[2]")
		}},
		{"server/CallToolRequest", func() error {
			return grpcserver.ValidateCallToolRequest(&registrypb.CallToolRequest{}, "batch", "[2]")
		}},
		{"server/ToolCallMeta", func() error {
			return grpcserver.ValidateToolCallMeta(&registrypb.ToolCallMeta{}, "batch", "[2]")
		}},
		{"server/RetryToolRequest", func() error {
			return grpcserver.ValidateRetryToolRequest(&registrypb.RetryToolRequest{}, "batch", "[2]")
		}},
		{"server/CompleteToolCallRequest", func() error {
			return grpcserver.ValidateCompleteToolCallRequest(&registrypb.CompleteToolCallRequest{}, "batch", "[2]")
		}},
		{"server/PublishToolOutputDeltaRequest", func() error {
			return grpcserver.ValidatePublishToolOutputDeltaRequest(&registrypb.PublishToolOutputDeltaRequest{}, "batch", "[2]")
		}},
		{"server/ReportToolCallOverloadRequest", func() error {
			return grpcserver.ValidateReportToolCallOverloadRequest(&registrypb.ReportToolCallOverloadRequest{}, "batch", "[2]")
		}},
		{"server/ClaimToolCallRequest", func() error {
			return grpcserver.ValidateClaimToolCallRequest(&registrypb.ClaimToolCallRequest{}, "batch", "[2]")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, tc.validate(), "batch[2]")
		})
	}
}
