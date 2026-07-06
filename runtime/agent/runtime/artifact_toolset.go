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
		// MaxArtifactBytes caps load_artifact responses. Zero means no configured cap.
		MaxArtifactBytes int
		// MaxArtifacts caps list_artifacts responses. Zero means no configured cap.
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
	artifactToolList = "list_artifacts"
	artifactToolLoad = "load_artifact"
)

// NewArtifactToolsetRegistration exposes persisted run artifacts as ordinary
// model tools.
func NewArtifactToolsetRegistration(cfg ArtifactToolsetConfig) ToolsetRegistration {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "artifacts"
	}
	return ToolsetRegistration{
		Name:        name,
		Description: "Model-facing tools for listing and loading persisted run artifacts.",
		Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return executeArtifactTool(ctx, cfg, call)
		},
		Specs: []tools.ToolSpec{
			artifactToolSpec(name, artifactToolList, "List persisted artifacts for the current run."),
			artifactToolSpec(name, artifactToolLoad, "Load bounded content from a persisted artifact."),
		},
	}
}

func executeArtifactTool(ctx context.Context, cfg ArtifactToolsetConfig, call *planner.ToolRequest) (*ToolExecutionResult, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("artifact store is required")
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

func executeListArtifacts(ctx context.Context, cfg ArtifactToolsetConfig, call *planner.ToolRequest, payload artifactListPayload) (*ToolExecutionResult, error) {
	limit := payload.Limit
	if cfg.MaxArtifacts > 0 && (limit <= 0 || limit > cfg.MaxArtifacts) {
		limit = cfg.MaxArtifacts
	}
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
	maxBytes := payload.MaxBytes
	if cfg.MaxArtifactBytes > 0 && (maxBytes <= 0 || maxBytes > cfg.MaxArtifactBytes) {
		maxBytes = cfg.MaxArtifactBytes
	}
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
