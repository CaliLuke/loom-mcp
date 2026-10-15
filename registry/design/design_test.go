package design

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryDesignTransportBoundaries(t *testing.T) {
	require.NoError(t, eval.Register(expr.Root))
	require.NoError(t, eval.Register(expr.GeneratedResultTypes))
	require.NoError(t, eval.RunDSL())
	service := expr.Root.Service("registry")
	require.NotNil(t, service)
	require.Len(t, service.Methods, 14)
	for _, name := range []string{"Register", "CallTool", "RetryTool"} {
		t.Run(name+" wire version", func(t *testing.T) {
			method := service.Method(name)
			require.NotNil(t, method)
			version := method.Payload.Find("wire_protocol_version")
			require.NotNil(t, version)
			require.NotNil(t, version.Validation)
			assert.Equal(t, []any{toolregistry.WireProtocolVersion}, version.Validation.Values,
				"transport must reject an incompatible wire protocol before routing")
		})
	}
	for _, tc := range []struct {
		method string
		field  string
	}{
		{"Register", "provider_incarnation_id"},
		{"ReleaseProvider", "provider_incarnation_id"},
		{"DrainProvider", "provider_incarnation_id"},
		{"Pong", "provider_incarnation_id"},
		{"CompleteToolCall", "provider_incarnation_id"},
		{"ClaimToolCall", "provider_incarnation_id"},
	} {
		t.Run(tc.method+" incarnation", func(t *testing.T) {
			method := service.Method(tc.method)
			require.NotNil(t, method)
			field := method.Payload.Find(tc.field)
			require.NotNil(t, field)
			require.NotNil(t, field.Validation)
			assert.EqualValues(t, expr.FormatUUID, field.Validation.Format,
				"provider lifecycle ownership requires a validated incarnation identity")
		})
	}
	for _, tc := range []struct {
		method string
		field  string
	}{
		{"Unregister", "expected_registration_token"},
		{"RetryTool", "expected_registration_token"},
		{"CompleteToolCall", "registration_token"},
		{"ClaimToolCall", "call_registration_token"},
		{"ClaimToolCall", "provider_registration_token"},
	} {
		t.Run(tc.method+" "+tc.field, func(t *testing.T) {
			field := service.Method(tc.method).Payload.Find(tc.field)
			require.NotNil(t, field)
			require.NotNil(t, field.Validation)
			assert.Equal(t, toolregistry.RegistrationTokenPattern, field.Validation.Pattern)
		})
	}
}
