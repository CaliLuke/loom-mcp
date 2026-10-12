package summarize

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type earlierCitation struct{}

func TestSummaryEarlierRunCitation(t *testing.T) {
	out, err := Validate(ValidationInput{Text: "Correction [r2 message 3].", CurrentPos: 3, To: 2, SummaryBytes: 1000, ReplacedTokens: 1000, Resolver: earlierCitation{}, RenderedCheckpoint: func(string) (int, error) {
		return 1, nil
	}})
	require.NoError(t, err)
	require.Len(t, out.CitedUsers, 1)
	assert.Equal(t, 2, out.CitedUsers[0].Origin.Pos)
}
func (earlierCitation) Resolve(key checkpoint.OrderKey) (checkpoint.CitationTarget, bool, error) {
	return checkpoint.CitationTarget{Kind: index.Message, Role: model.ConversationRoleUser, Line: checkpoint.UserLine{ID: key.ID, Text: "earlier correction", Origin: &index.Origin{Pos: key.Pos, ID: key.ID}}}, true, nil
}
func (earlierCitation) Behind(checkpoint.OrderKey) bool {
	return true
}
