package registry_test

import (
	"strings"

	grpcclient "github.com/CaliLuke/loom-mcp/v2/registry/gen/grpc/registry/client"
	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/toolregistry"
	loom "github.com/CaliLuke/loom/pkg"
)

// registryTransportCases pin the public requests and responses, including
// optional metadata that must survive client/server conversion.
func registryTransportCases() []registryTransportCase {
	token := strings.Repeat("a", 64)
	incarnation := "8af45fe9-5c32-4b46-8da5-d350e98b68f3"
	description := "Search retained documents"
	version := genregistry.SemVer("1.2.3-beta.1")
	turn, parent := "turn-7", "parent-call"
	meta := &genregistry.ToolCallMeta{RunID: "run-1", SessionID: "session-2", ToolCallID: "call-3", TurnID: &turn, ParentToolCallID: &parent}
	schema := &genregistry.ToolSchema{Name: "docs.search", Description: &description, Tags: []string{"read"}, PayloadSchema: []byte(`{"type":"object"}`), ResultSchema: []byte(`{"type":"array"}`), SidecarSchema: []byte(`{"type":"string"}`)}
	info := &genregistry.ToolsetInfo{Name: "docs", Description: &description, Version: &version, Tags: []string{"read"}, ToolCount: 1, RegisteredAt: "2026-10-14T00:00:00Z"}
	callResult := &genregistry.CallToolResult{ToolUseID: "use-1", RegistrationToken: token, ExecutionDeadline: "2026-10-14T00:01:00Z", ResultStreamExpiresAt: "2026-10-14T00:15:00Z"}
	claim := &genregistry.ProviderToolCallClaimPayload{Toolset: "docs", ProviderID: "provider-1", ProviderIncarnationID: incarnation, ProviderRegistrationToken: token, CallRegistrationToken: token, ToolUseID: "use-1", RequestEventID: "123-0"}
	return []registryTransportCase{
		{
			name: "Register", command: "register",
			payload: &genregistry.RegisterPayload{Name: "docs", Description: &description, Version: &version, Tags: []string{"read"}, Tools: []*genregistry.ToolSchema{schema}, ProviderID: "provider-1", AdmissionRevision: "release-1", ProviderIncarnationID: incarnation, WireProtocolVersion: toolregistry.WireProtocolVersion},
			result:  &genregistry.RegisterResult{RegisteredAt: "2026-10-14T00:00:00Z", RegistrationToken: token, LeaseDurationMs: 60000},
			build:   registryPayloadBuilder(grpcclient.BuildRegisterPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.Register = f
			},
			client: (*grpcclient.Client).Register,
			errors: []string{"admission_blocked", "admission_retired", "validation_error", "service_unavailable"},
		},
		{
			name: "ReleaseProvider", command: "release-provider",
			payload: &genregistry.ReleaseProviderPayload{Name: "docs", ProviderID: "provider-1", ExpectedRegistrationToken: token, ProviderIncarnationID: incarnation},
			build:   registryPayloadBuilder(grpcclient.BuildReleaseProviderPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.ReleaseProvider = f
			},
			client: (*grpcclient.Client).ReleaseProvider,
			errors: []string{"service_unavailable"},
		},
		{
			name: "DrainProvider", command: "drain-provider",
			payload: &genregistry.DrainProviderPayload{Name: "docs", ProviderID: "provider-1", ExpectedRegistrationToken: token, ProviderIncarnationID: incarnation, SettlementDurationMs: 30000},
			build:   registryPayloadBuilder(grpcclient.BuildDrainProviderPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.DrainProvider = f
			},
			client: (*grpcclient.Client).DrainProvider,
			errors: []string{"service_unavailable"},
		},
		{
			name: "Unregister", command: "unregister",
			payload: &genregistry.UnregisterPayload{Name: "docs", ExpectedRegistrationToken: token},
			build:   registryPayloadBuilder(grpcclient.BuildUnregisterPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.Unregister = f
			},
			client: (*grpcclient.Client).Unregister,
			errors: []string{"admission_conflict", "service_unavailable"},
		},
		{
			name: "Pong", command: "pong",
			payload: &genregistry.PongPayload{PingID: "ping-1", Toolset: "docs", ProviderID: "provider-1", ProviderIncarnationID: incarnation},
			build:   registryPayloadBuilder(grpcclient.BuildPongPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.Pong = f
			},
			client: (*grpcclient.Client).Pong,
		},
		{
			name: "ListToolsets", command: "list-toolsets",
			payload: &genregistry.ListToolsetsPayload{Tags: []string{"read", "docs"}},
			result:  &genregistry.ListToolsetsResult{Toolsets: []*genregistry.ToolsetInfo{info}},
			build:   registryPayloadBuilder(grpcclient.BuildListToolsetsPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.ListToolsets = f
			},
			client: (*grpcclient.Client).ListToolsets,
		},
		{
			name: "GetToolset", command: "get-toolset",
			payload: &genregistry.GetToolsetPayload{Name: "docs"},
			result:  &genregistry.Toolset{Name: "docs", Description: &description, Version: &version, Tags: []string{"read"}, Tools: []*genregistry.ToolSchema{schema}, RegisteredAt: "2026-10-14T00:00:00Z"},
			build:   registryPayloadBuilder(grpcclient.BuildGetToolsetPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.GetToolset = f
			},
			client: (*grpcclient.Client).GetToolset,
			errors: []string{"not_found"},
		},
		{
			name: "Search", command: "search",
			payload: &genregistry.SearchPayload{Query: "retention"},
			result:  &genregistry.SearchResult{Toolsets: []*genregistry.ToolsetInfo{info}},
			build:   registryPayloadBuilder(grpcclient.BuildSearchPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.Search = f
			},
			client: (*grpcclient.Client).Search,
		},
		{
			name: "CallTool", command: "call-tool",
			payload: &genregistry.CallToolPayload{Toolset: "docs", Tool: "docs.search", PayloadJSON: []byte(`{"query":"retention"}`), Meta: meta, WireProtocolVersion: toolregistry.WireProtocolVersion},
			result:  callResult,
			build:   registryPayloadBuilder(grpcclient.BuildCallToolPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.CallTool = f
			},
			client: (*grpcclient.Client).CallTool,
			errors: []string{"not_found", "validation_error", "service_unavailable", "call_not_admitted"},
		},
		{
			name: "RetryTool", command: "retry-tool",
			payload: &genregistry.RetryToolPayload{ExpectedRegistrationToken: token, Toolset: "docs", Tool: "docs.search", PayloadJSON: []byte(`{"query":"retention"}`), Meta: meta, WireProtocolVersion: toolregistry.WireProtocolVersion},
			result:  callResult,
			build:   registryPayloadBuilder(grpcclient.BuildRetryToolPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.RetryTool = f
			},
			client: (*grpcclient.Client).RetryTool,
			errors: []string{"not_found", "validation_error", "service_unavailable", "admission_conflict"},
		},
		{
			name: "CompleteToolCall", command: "complete-tool-call",
			payload: &genregistry.CompleteToolCallPayload{Toolset: "docs", ProviderID: "provider-1", ProviderIncarnationID: incarnation, RegistrationToken: token, ToolUseID: "use-1", ResultJSON: []byte(`{"result":"ok"}`), RequestEventID: "123-0", ProviderRegistrationToken: token},
			build:   registryPayloadBuilder(grpcclient.BuildCompleteToolCallPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.CompleteToolCall = f
			},
			client: (*grpcclient.Client).CompleteToolCall,
			errors: []string{"validation_error", "service_unavailable"},
		},
		{
			name: "PublishToolOutputDelta", command: "publish-tool-output-delta",
			payload: &genregistry.PublishToolOutputDeltaPayload{Stream: "stdout", Delta: "searching", Toolset: "docs", ProviderID: "provider-1", ProviderIncarnationID: incarnation, ProviderRegistrationToken: token, CallRegistrationToken: token, ToolUseID: "use-1", RequestEventID: "123-0"},
			build:   registryPayloadBuilder(grpcclient.BuildPublishToolOutputDeltaPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.PublishToolOutputDelta = f
			},
			client: (*grpcclient.Client).PublishToolOutputDelta,
			errors: []string{"validation_error", "service_unavailable"},
		},
		{
			name: "ReportToolCallOverload", command: "report-tool-call-overload", payload: claim,
			build: registryPayloadBuilder(grpcclient.BuildReportToolCallOverloadPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.ReportToolCallOverload = f
			},
			client: (*grpcclient.Client).ReportToolCallOverload,
			errors: []string{"validation_error", "service_unavailable"},
		},
		{
			name: "ClaimToolCall", command: "claim-tool-call", payload: claim,
			result: &genregistry.ClaimToolCallResult{Disposition: "execute"},
			build:  registryPayloadBuilder(grpcclient.BuildClaimToolCallPayload),
			bind: func(e *genregistry.Endpoints, f loom.Endpoint) {
				e.ClaimToolCall = f
			},
			client: (*grpcclient.Client).ClaimToolCall,
			errors: []string{"validation_error", "service_unavailable"},
		},
	}
}
