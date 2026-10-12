package fake_test

import (
	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCallIDsDoNotReuseCompactedMessageCount(t *testing.T) {
	p := &fake.Reader{}
	ids := make([]string, 0, 2)
	for _, step := range []string{"r1.1", "r1.2"} {
		req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "NEXT:{\"tool\":\"ops_deployer.read_service_log\",\"step\":\"" + step + "\"}"}}}}}
		r, err := p.Complete(t.Context(), req)
		require.NoError(t, err)
		ids = append(ids, r.ToolCalls[0].ID)
	}
	assert.NotEqual(t, ids[0], ids[1])
}
