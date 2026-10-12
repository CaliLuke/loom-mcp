package evidence

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecalledAttachmentsRequireReceivedBytes(t *testing.T) {
	for _, part := range []model.ToolResultContentPart{model.ImagePart{Format: "png", Bytes: []byte("asset")}, model.DocumentPart{Format: "pdf", Bytes: []byte("asset")}} {
		for _, tc := range []struct {
			name           string
			bytes, adopted bool
		}{{"received", true, true}, {"descriptor_only", false, true}, {"abandoned", true, false}} {
			t.Run(fmt.Sprintf("%T/%s", part, tc.name), func(t *testing.T) {
				s := spec()
				s.Evidence["fact"] = Evidence{Asset: fmt.Sprintf("%x", sha256.Sum256([]byte("asset"))), Token: "ABC-12345"}
				result := model.ToolResultPart{ToolUseID: "recall", Content: map[string]any{"attachment": map[string]any{"message": "1", "part": 2}}}
				if tc.bytes {
					result.Parts = []model.ToolResultContentPart{part}
				}
				received := call(2, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolUsePart{ID: "recall", Name: "runtime.recall_context"}, result}})
				if !tc.adopted {
					received.Class = runrecord.Abandoned
				}
				r := record(call(1, msg("NEED:")), received)
				r.Runs[0].Transcript = []*model.Message{answer("ABC-12345")}
				got, err := Classify([]*runrecord.RunRecord{r}, s, true)
				require.NoError(t, err)
				assert.True(t, got.AbsentWhenNeeded)
				assert.Equal(t, tc.bytes && tc.adopted, got.RecallSuccess)
				assert.Equal(t, !tc.bytes || !tc.adopted, got.UnsupportedClaim)
				if tc.bytes && tc.adopted {
					assert.Contains(t, got.Sources, Recall)
				}
			})
		}
	}
}
