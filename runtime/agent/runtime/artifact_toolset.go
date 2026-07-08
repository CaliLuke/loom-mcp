package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// ArtifactToolsetConfig configures model-facing artifact tools.
	ArtifactToolsetConfig struct {
		// Name is the toolset name used to construct canonical tool IDs.
		Name string
		// Store is the artifact store used by the generated tools.
		Store artifact.Store
		// MaxArtifactBytes caps load_artifact responses. Zero uses DefaultArtifactLoadMaxBytes.
		// Set UnlimitedToolsetLimit to disable the runtime ceiling.
		MaxArtifactBytes int
		// MaxArtifacts caps list_artifacts responses. Zero uses DefaultArtifactListLimit.
		// Set UnlimitedToolsetLimit to disable the runtime ceiling.
		MaxArtifacts int
	}

	artifactListPayload struct {
		MimeType string            `json:"mime_type,omitempty"`
		Metadata map[string]string `json:"metadata,omitempty"`
		Limit    int               `json:"limit,omitempty"`
	}

	artifactListResult struct {
		Artifacts []artifact.Ref `json:"artifacts"`
	}

	artifactLoadPayload struct {
		ID       string `json:"id"`
		MaxBytes int    `json:"max_bytes,omitempty"`
	}

	artifactLoadResult struct {
		Content   string `json:"content"`
		MimeType  string `json:"mime_type,omitempty"`
		Truncated bool   `json:"truncated"`
		SizeBytes int64  `json:"size_bytes"`
	}
)

const (
	artifactDefaultToolsetName = "artifacts"
	artifactToolList           = "list_artifacts"
	artifactToolLoad           = "load_artifact"

	// DefaultArtifactListLimit bounds listed artifact refs when MaxArtifacts is unset.
	DefaultArtifactListLimit = 100
	// DefaultArtifactLoadMaxBytes bounds loaded artifact content when MaxArtifactBytes is unset.
	DefaultArtifactLoadMaxBytes = 256 * 1024
)

// NewArtifactToolsetRegistration exposes persisted run artifacts as ordinary
// model tools.
func NewArtifactToolsetRegistration(cfg ArtifactToolsetConfig) ToolsetRegistration {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = artifactDefaultToolsetName
	}
	var specs []tools.ToolSpec
	if cfg.Store != nil {
		specs = []tools.ToolSpec{
			artifactToolSpec(name, artifactToolList, "List persisted artifacts for the current run."),
			artifactToolSpec(name, artifactToolLoad, "Load bounded content from a persisted artifact."),
		}
	}
	return ToolsetRegistration{
		Name:        name,
		Description: "Model-facing tools for listing and loading persisted run artifacts.",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return executeArtifactTool(ctx, cfg, call)
		},
		Specs: specs,
	}
}

func executeArtifactTool(ctx context.Context, cfg ArtifactToolsetConfig, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	if call == nil {
		return nil, fmt.Errorf("artifact tool request is nil")
	}
	if cfg.Store == nil {
		return unsupportedArtifactStore(call), nil
	}
	switch call.Name.Tool() {
	case artifactToolList:
		var payload artifactListPayload
		if err := decodeArtifactPayload(call, &payload); err != nil {
			return nil, err
		}
		return executeListArtifacts(ctx, cfg, call, payload)
	case artifactToolLoad:
		var payload artifactLoadPayload
		if err := decodeArtifactPayload(call, &payload); err != nil {
			return nil, err
		}
		return executeLoadArtifact(ctx, cfg, call, payload)
	default:
		return nil, fmt.Errorf("unknown artifact tool %q", call.Name)
	}
}

func unsupportedArtifactStore(call *planner.ToolRequest) *ToolExecutionResult {
	message := "Configure runtime.WithArtifactStore to enable artifact listing and loading."
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      planner.NewToolError(message),
		RetryHint: &planner.RetryHint{
			Reason:         planner.RetryReasonUnsupportedOperation,
			Tool:           call.Name,
			RestrictToTool: true,
			Message:        message,
		},
	})
}

func executeListArtifacts(ctx context.Context, cfg ArtifactToolsetConfig, call *planner.ToolRequest, payload artifactListPayload) (*ToolExecutionResult, error) {
	limit := artifactListLimit(payload.Limit, cfg.MaxArtifacts)
	refs, err := cfg.Store.List(ctx, artifact.ListQuery{
		AgentID:  string(call.AgentID),
		RunID:    call.RunID,
		MimeType: payload.MimeType,
		Metadata: payload.Metadata,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Result:     artifactListResult{Artifacts: refs},
	}), nil
}

func executeLoadArtifact(ctx context.Context, cfg ArtifactToolsetConfig, call *planner.ToolRequest, payload artifactLoadPayload) (*ToolExecutionResult, error) {
	if strings.TrimSpace(payload.ID) == "" {
		return nil, fmt.Errorf("artifact id is required")
	}
	maxBytes := artifactLoadMaxBytes(payload.MaxBytes, cfg.MaxArtifactBytes)
	content, err := cfg.Store.Load(ctx, artifact.LoadQuery{
		AgentID:  string(call.AgentID),
		RunID:    call.RunID,
		ID:       payload.ID,
		MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, err
	}
	return Executed(&planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Result: artifactLoadResult{
			Content:   string(content.Body),
			MimeType:  content.Ref.MimeType,
			Truncated: content.Truncated,
			SizeBytes: content.SizeBytes,
		},
	}), nil
}

func artifactListLimit(requested, configured int) int {
	return toolsetLimit(requested, configured, DefaultArtifactListLimit)
}

func artifactLoadMaxBytes(requested, configured int) int {
	return toolsetLimit(requested, configured, DefaultArtifactLoadMaxBytes)
}

func toolsetLimit(requested, configured, defaultLimit int) int {
	if configured == UnlimitedToolsetLimit {
		return requested
	}
	ceiling := defaultLimit
	if configured > 0 {
		ceiling = configured
	}
	if requested <= 0 || requested > ceiling {
		return ceiling
	}
	return requested
}

func decodeArtifactPayload(call *planner.ToolRequest, out any) error {
	if len(call.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(call.Payload.RawMessage(), out)
}

func artifactToolSpec(toolset, tool, description string) tools.ToolSpec {
	id := tools.Ident(toolset + "." + tool)
	return tools.ToolSpec{
		Name:        id,
		Toolset:     toolset,
		Description: description,
		Payload:     tools.TypeSpec{Codec: tools.AnyJSONCodec},
		Result:      tools.TypeSpec{Codec: tools.AnyJSONCodec},
	}
}
