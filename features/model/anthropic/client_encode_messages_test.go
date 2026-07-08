package anthropic

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func TestEncodeMessages_RewritesUnknownToolUseToToolUnavailable(t *testing.T) {
	nameMap := map[string]string{
		tools.ToolUnavailable.String(): sanitizeToolName(tools.ToolUnavailable.String()),
	}
	_, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.ToolUsePart{
					ID:    "tu1",
					Name:  "atlas_read_count_events",
					Input: map[string]any{"from": "2026-02-06T00:00:00Z"},
				},
			},
		},
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.ToolResultPart{
					ToolUseID: "tu1",
					Content:   map[string]any{"error": "unknown tool"},
					IsError:   true,
				},
			},
		},
	}, nameMap)
	if err != nil {
		t.Fatalf("encodeMessages error: %v", err)
	}
}

func TestEncodeMessages_ReturnsToolResultMarshalError(t *testing.T) {
	_, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.ToolResultPart{
					ToolUseID: "tu1",
					Content:   map[string]any{"bad": make(chan int)},
				},
			},
		},
	}, nil)
	if err == nil {
		t.Fatal("encodeMessages returned nil error")
	}
	if !strings.Contains(err.Error(), `anthropic: encode tool result "tu1"`) {
		t.Fatalf("encodeMessages error = %q, want tool result context", err.Error())
	}
}
