package fake_test

import (
	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestReaderRejectsUnusableOmittedResult(t *testing.T) {
	req := &model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: `NEXT:{"tool":"ops_deployer.list_services","step":"r1.0"}`}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "one", Content: map[string]any{"omitted": true, "reason": "size_limit"}}}},
	}}
	_, err := (&fake.Reader{}).Complete(t.Context(), req)
	assert.ErrorContains(t, err, "size-limit omission")
}
