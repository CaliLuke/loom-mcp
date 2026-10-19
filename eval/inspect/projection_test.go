package inspect

import (
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalProjectionMandatoryExclusionsPrecedeHostApproval(t *testing.T) {
	message := &model.Message{Role: model.ConversationRoleUser, Meta: map[string]any{"private": "metadata-secret"}, Parts: []model.Part{
		model.TextPart{Text: "instruction-bearing task data"}, model.ThinkingPart{Text: "reasoning-secret"}, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("attachment-secret")}, model.ToolResultPart{ToolUseID: "tool", Content: map[string]any{"value": "approved-result"}},
	}}
	system := &model.Message{Role: "system", Parts: []model.Part{model.TextPart{Text: "system-secret"}}}
	record := &runrecord.RunRecord{RootRunID: "root", Content: map[runstore.Hash][]byte{"hidden": []byte("raw-secret")}, Runs: []runrecord.Run{{RunID: "root", Sealed: true, Transcript: []*model.Message{system, message}, ServerOnly: []runrecord.ContentPart{{Content: []byte(`"server-secret"`)}}}}}
	before, err := record.Export().Marshal()
	require.NoError(t, err)
	policy := Policy{Role: Editor, Name: "allow-everything/v1", RunContent: func(c runrecord.ProjectionContent) (jsontext.Value, bool, error) { return c.Value, true, nil }}
	projected, err := projectRecord(record, policy)
	require.NoError(t, err)
	body, err := projected.Marshal()
	require.NoError(t, err)
	assert.Contains(t, string(body), "instruction-bearing task data")
	assert.Contains(t, string(body), "approved-result")
	for _, secret := range []string{"metadata-secret", "reasoning-secret", "attachment-secret", "system-secret", "raw-secret", "server-secret"} {
		assert.NotContains(t, string(body), secret)
	}
	assert.NotEmpty(t, projected.Omissions)
	after, err := record.Export().Marshal()
	require.NoError(t, err)
	assert.Equal(t, before, after)
	// Nil host approval is fail-closed, even for ordinary public leaves.
	policy.RunContent = nil
	projected, err = projectRecord(record, policy)
	require.NoError(t, err)
	body, err = projected.Marshal()
	require.NoError(t, err)
	assert.NotContains(t, string(body), "instruction-bearing task data")
}

func TestCanonicalProjectionHostFailureDoesNotExposeUnsafeDiagnostic(t *testing.T) {
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Transcript: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "data"}}}}}}}
	projected, err := projectRecord(record, Policy{Role: Editor, Name: "broken/v1", RunContent: func(runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return nil, false, errors.New("secret host path")
	}})
	require.Error(t, err)
	require.NotNil(t, projected)
	assert.True(t, projected.Incomplete)
	body, marshalErr := projected.Marshal()
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(body), "secret host path")
}

func TestCanonicalUnsealedRunIsExplicitlyIncomplete(t *testing.T) {
	f := newFixture(t)
	b := publicationBuilder{workspace: f.w, input: f.pub, blobs: map[string][]byte{}}
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Sealed: false}}}
	require.NoError(t, b.addRecordViews(record, Policy{Role: Editor, Name: "editor/v1"}, false))
	require.Len(t, b.entries, 1)
	assert.True(t, b.entries[0].entry.Incomplete)
}
