package registry_test

import (
	"testing"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	grpcserver "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/server"
	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryConversionsRejectNullCollectionElements(t *testing.T) {
	for _, tc := range []struct {
		name    string
		convert func() (any, error)
	}{
		{"encode registration", func() (any, error) {
			return grpcclient.NewProtoRegisterRequest(&genregistry.RegisterPayload{Tools: []*genregistry.ToolSchema{nil}})
		}},
		{"decode registration", func() (any, error) {
			return grpcserver.NewRegisterPayload(&registrypb.RegisterRequest{Name: new("docs"), ProviderId: new("provider"), AdmissionRevision: new("revision"), ProviderIncarnationId: new("id"), WireProtocolVersion: new(int64(1)), Tools: []*registrypb.ToolSchema{nil}})
		}},
		{"encode list", func() (any, error) {
			return grpcserver.NewProtoListToolsetsResponse(&genregistry.ListToolsetsResult{Toolsets: []*genregistry.ToolsetInfo{nil}})
		}},
		{"decode list", func() (any, error) {
			return grpcclient.NewListToolsetsResult(&registrypb.ListToolsetsResponse{Toolsets: []*registrypb.ToolsetInfo{nil}})
		}},
		{"encode search", func() (any, error) {
			return grpcserver.NewProtoSearchResponse(&genregistry.SearchResult{Toolsets: []*genregistry.ToolsetInfo{nil}})
		}},
		{"decode search", func() (any, error) {
			return grpcclient.NewSearchResult(&registrypb.SearchResponse{Toolsets: []*registrypb.ToolsetInfo{nil}})
		}},
		{"encode toolset", func() (any, error) {
			return grpcserver.NewProtoGetToolsetResponse(&genregistry.Toolset{Tools: []*genregistry.ToolSchema{nil}})
		}},
		{"decode toolset", func() (any, error) {
			return grpcclient.NewGetToolsetResult(&registrypb.GetToolsetResponse{Name: new("docs"), RegisteredAt: new("2026-10-14T00:00:00Z"), Tools: []*registrypb.ToolSchema{nil}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.convert()
			require.ErrorContains(t, err, "nil message collection element")
			assert.Nil(t, out, "failed conversion must not expose a partial request or result")
		})
	}
}
