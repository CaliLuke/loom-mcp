package registry_test

import (
	"context"
	"testing"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	grpcserver "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// The exported codecs are extension points used by custom transport wiring.
// An incompatible wire or domain value must fail before any type conversion.
func TestRegistryCodecsRejectMismatchedTypes(t *testing.T) {
	decoders := map[string]func(context.Context, any, metadata.MD) (any, error){
		"DecodeRegisterRequest":               grpcserver.DecodeRegisterRequest,
		"DecodeReleaseProviderRequest":        grpcserver.DecodeReleaseProviderRequest,
		"DecodeDrainProviderRequest":          grpcserver.DecodeDrainProviderRequest,
		"DecodeUnregisterRequest":             grpcserver.DecodeUnregisterRequest,
		"DecodePongRequest":                   grpcserver.DecodePongRequest,
		"DecodeListToolsetsRequest":           grpcserver.DecodeListToolsetsRequest,
		"DecodeGetToolsetRequest":             grpcserver.DecodeGetToolsetRequest,
		"DecodeSearchRequest":                 grpcserver.DecodeSearchRequest,
		"DecodeCallToolRequest":               grpcserver.DecodeCallToolRequest,
		"DecodeRetryToolRequest":              grpcserver.DecodeRetryToolRequest,
		"DecodeCompleteToolCallRequest":       grpcserver.DecodeCompleteToolCallRequest,
		"DecodePublishToolOutputDeltaRequest": grpcserver.DecodePublishToolOutputDeltaRequest,
		"DecodeReportToolCallOverloadRequest": grpcserver.DecodeReportToolCallOverloadRequest,
		"DecodeClaimToolCallRequest":          grpcserver.DecodeClaimToolCallRequest,
	}
	for name, decode := range decoders {
		t.Run(name, func(t *testing.T) {
			result, err := decode(t.Context(), struct{}{}, nil)
			require.ErrorContains(t, err, "invalid value expected")
			assert.Nil(t, result)
		})
	}
	responses := map[string]func(context.Context, any, metadata.MD, metadata.MD) (any, error){
		"DecodeRegisterResponse":      grpcclient.DecodeRegisterResponse,
		"DecodeListToolsetsResponse":  grpcclient.DecodeListToolsetsResponse,
		"DecodeGetToolsetResponse":    grpcclient.DecodeGetToolsetResponse,
		"DecodeSearchResponse":        grpcclient.DecodeSearchResponse,
		"DecodeCallToolResponse":      grpcclient.DecodeCallToolResponse,
		"DecodeRetryToolResponse":     grpcclient.DecodeRetryToolResponse,
		"DecodeClaimToolCallResponse": grpcclient.DecodeClaimToolCallResponse,
	}
	for name, decode := range responses {
		t.Run(name, func(t *testing.T) {
			result, err := decode(t.Context(), struct{}{}, nil, nil)
			require.ErrorContains(t, err, "invalid value expected")
			assert.Nil(t, result)
		})
	}

	encoders := map[string]func(context.Context, any, *metadata.MD, *metadata.MD) (any, error){
		"EncodeRegisterResponse":      grpcserver.EncodeRegisterResponse,
		"EncodeListToolsetsResponse":  grpcserver.EncodeListToolsetsResponse,
		"EncodeGetToolsetResponse":    grpcserver.EncodeGetToolsetResponse,
		"EncodeSearchResponse":        grpcserver.EncodeSearchResponse,
		"EncodeCallToolResponse":      grpcserver.EncodeCallToolResponse,
		"EncodeRetryToolResponse":     grpcserver.EncodeRetryToolResponse,
		"EncodeClaimToolCallResponse": grpcserver.EncodeClaimToolCallResponse,
	}
	for name, encode := range encoders {
		t.Run(name, func(t *testing.T) {
			result, err := encode(t.Context(), struct{}{}, nil, nil)
			require.ErrorContains(t, err, "invalid value expected")
			assert.Nil(t, result)
		})
	}
}
