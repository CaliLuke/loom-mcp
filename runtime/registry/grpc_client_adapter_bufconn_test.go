package registry

import (
	"context"
	"net"
	"testing"

	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	grpcserver "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/server"
	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCClientAdapterGeneratedServerContract(t *testing.T) {
	service := &registryServiceStub{}
	endpoints := genregistry.NewEndpoints(service)
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	registrypb.RegisterRegistryServer(server, grpcserver.New(endpoints, nil))
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, <-serveErr)
		require.NoError(t, listener.Close())
	})

	conn, err := grpc.NewClient(
		"passthrough:///registry",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, conn.Close())
	})
	adapter := NewGRPCClientAdapter(registrypb.NewRegistryClient(conn))

	toolsets, err := adapter.ListToolsets(t.Context())
	require.NoError(t, err)
	require.Len(t, toolsets, 1)
	assert.Equal(t, "analytics", toolsets[0].Name)
	assert.Equal(t, "1.2.3", toolsets[0].Version)
	assert.Equal(t, []string{"data"}, toolsets[0].Tags)

	schema, err := adapter.GetToolset(t.Context(), "analytics")
	require.NoError(t, err)
	assert.Equal(t, "analytics", service.getName)
	require.Len(t, schema.Tools, 1)
	assert.Equal(t, "analytics.query", schema.Tools[0].Name)
	assert.JSONEq(t, "{\"type\":\"object\"}", string(schema.Tools[0].PayloadSchema))
	assert.JSONEq(t, "{\"type\":\"array\"}", string(schema.Tools[0].ResultSchema))
	assert.JSONEq(t, "{\"type\":\"object\"}", string(schema.Tools[0].SidecarSchema))

	results, err := adapter.Search(t.Context(), "warehouse")
	require.NoError(t, err)
	assert.Equal(t, "warehouse", service.searchQuery)
	require.Len(t, results, 1)
	assert.Equal(t, "analytics", results[0].Name)
	assert.Equal(t, searchResultTypeToolset, results[0].Type)
}

type registryServiceStub struct {
	getName     string
	searchQuery string
}

func (*registryServiceStub) Register(context.Context, *genregistry.RegisterPayload) (*genregistry.RegisterResult, error) {
	return nil, nil
}

func (*registryServiceStub) ReleaseProvider(context.Context, *genregistry.ReleaseProviderPayload) error {
	return nil
}

func (*registryServiceStub) DrainProvider(context.Context, *genregistry.DrainProviderPayload) error {
	return nil
}

func (*registryServiceStub) Unregister(context.Context, *genregistry.UnregisterPayload) error {
	return nil
}

func (*registryServiceStub) Pong(context.Context, *genregistry.PongPayload) error {
	return nil
}

func (*registryServiceStub) ListToolsets(context.Context, *genregistry.ListToolsetsPayload) (*genregistry.ListToolsetsResult, error) {
	description := "Warehouse analytics"
	version := genregistry.SemVer("1.2.3")
	return &genregistry.ListToolsetsResult{Toolsets: []*genregistry.ToolsetInfo{{
		Name:        "analytics",
		Description: &description,
		Version:     &version,
		Tags:        []string{"data"},
		ToolCount:   1,
	}}}, nil
}

func (s *registryServiceStub) GetToolset(_ context.Context, payload *genregistry.GetToolsetPayload) (*genregistry.Toolset, error) {
	s.getName = payload.Name
	description := "Warehouse analytics"
	toolDescription := "Query analytics data"
	version := genregistry.SemVer("1.2.3")
	return &genregistry.Toolset{
		Name:        "analytics",
		Description: &description,
		Version:     &version,
		Tools: []*genregistry.ToolSchema{{
			Name:          "analytics.query",
			Description:   &toolDescription,
			PayloadSchema: []byte("{\"type\":\"object\"}"),
			ResultSchema:  []byte("{\"type\":\"array\"}"),
			SidecarSchema: []byte("{\"type\":\"object\"}"),
		}},
	}, nil
}

func (s *registryServiceStub) Search(_ context.Context, payload *genregistry.SearchPayload) (*genregistry.SearchResult, error) {
	s.searchQuery = payload.Query
	description := "Warehouse analytics"
	return &genregistry.SearchResult{Toolsets: []*genregistry.ToolsetInfo{{
		Name:        "analytics",
		Description: &description,
		Tags:        []string{"data"},
	}}}, nil
}

func (*registryServiceStub) CallTool(context.Context, *genregistry.CallToolPayload) (*genregistry.CallToolResult, error) {
	return nil, nil
}

func (*registryServiceStub) RetryTool(context.Context, *genregistry.RetryToolPayload) (*genregistry.CallToolResult, error) {
	return nil, nil
}

func (*registryServiceStub) CompleteToolCall(context.Context, *genregistry.CompleteToolCallPayload) error {
	return nil
}

func (*registryServiceStub) PublishToolOutputDelta(context.Context, *genregistry.PublishToolOutputDeltaPayload) error {
	return nil
}

func (*registryServiceStub) ReportToolCallOverload(context.Context, *genregistry.ProviderToolCallClaimPayload) error {
	return nil
}

func (*registryServiceStub) ClaimToolCall(context.Context, *genregistry.ProviderToolCallClaimPayload) (*genregistry.ClaimToolCallResult, error) {
	return nil, nil
}
