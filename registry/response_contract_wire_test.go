package registry_test

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	registrypb "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/pb"
	grpcserver "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/server"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestRegistryDeclaredResponseContractsMatchObservedWire(t *testing.T) {
	contracts := grpcserver.ResponseContractCases()
	require.NotEmpty(t, contracts)
	service := registrypb.File_loomgen_registry_registry_proto.Services().Get(0)
	observed := make(map[string]bool, len(contracts))
	for _, tc := range registryTransportCases() {
		contractsForMethod := 0
		for _, contract := range contracts {
			if !strings.HasPrefix(contract.ID, "registry."+tc.name+".") {
				continue
			}
			contractsForMethod++
			t.Run(contract.ID, func(t *testing.T) {
				assert.False(t, observed[contract.ID], "contract IDs must identify one response")
				observed[contract.ID] = true
				conn := registryTransportConnection(t, tc, func(context.Context, any) (any, error) {
					if contract.ErrorName != "" {
						assert.Contains(t, tc.errors, contract.ErrorName)
						return nil, loom.PermanentError(contract.ErrorName, "public diagnostic")
					}
					return tc.result, nil
				})
				method := service.Methods().ByName(protoreflect.Name(tc.name))
				request := dynamicpb.NewMessage(method.Input())
				raw, err := json.Marshal(tc.payload)
				require.NoError(t, err)
				require.NoError(t, protojson.Unmarshal(raw, request))
				response := dynamicpb.NewMessage(method.Output())
				err = conn.Invoke(t.Context(), "/"+string(service.FullName())+"/"+tc.name, request, response)
				assert.Equal(t, contract.StatusCode, status.Code(err))
				if contract.ErrorName == "" {
					require.NoError(t, err)
					assert.Equal(t, contract.MessageType, string(response.Descriptor().FullName()))
				} else {
					require.Error(t, err)
					details := status.Convert(err).Proto().Details
					require.Len(t, details, 1)
					assert.True(t, strings.HasSuffix(details[0].TypeUrl, "/"+contract.DetailType), "declared error detail type must match the wire: %s", details[0].TypeUrl)
				}
			})
		}
		assert.Equal(t, 1+len(tc.errors), contractsForMethod, "%s must declare its success and every authored error", tc.name)
	}
	assert.Len(t, observed, len(contracts), "every declared contract needs an executable scenario")
}

func TestRegistryForwardCompatibleStubRejectsEveryRPC(t *testing.T) {
	server := grpc.NewServer()
	registrypb.RegisterRegistryServer(server, registrypb.UnimplementedRegistryServer{})
	conn := rawRegistryConnection(t, server)
	service := registrypb.File_loomgen_registry_registry_proto.Services().Get(0)
	for i := range service.Methods().Len() {
		method := service.Methods().Get(i)
		t.Run(string(method.Name()), func(t *testing.T) {
			err := conn.Invoke(t.Context(), "/"+string(service.FullName())+"/"+string(method.Name()), dynamicpb.NewMessage(method.Input()), dynamicpb.NewMessage(method.Output()))
			require.Error(t, err)
			assert.Equal(t, codes.Unimplemented, status.Code(err), "an unimplemented method must not claim success")
		})
	}
}
