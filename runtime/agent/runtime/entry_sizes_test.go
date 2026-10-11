package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedEntryModelFacingSizes(t *testing.T) {
	rt := New(WithRunStore(storeinmem.New()))
	messages := []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "é"}, model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}}, Meta: map[string]any{"ignored": "metadata"}}, nil}
	items, err := encodeContentMessages(messages)
	require.NoError(t, err)
	out, err := rt.recordContentItems(t.Context(), &api.RecordContentInput{RootRunID: "root", Items: items, SeedMessages: true})
	require.NoError(t, err)
	require.Len(t, out.Entries, 2)
	assert.Equal(t, 2, out.Entries[0].Bytes)
	assert.Equal(t, 1500, out.Entries[0].NonText)
	assert.Equal(t, model.ConversationRoleUser, out.Entries[0].Role)
	assert.Zero(t, out.Entries[1].Bytes)
}

func TestEntryValidationMeasuredFields(t *testing.T) {
	_, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.ToolResult, Bytes: 4, NonText: 1500, Keep: true}})
	require.NoError(t, err)
	_, err = index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.Message, Origin: &index.Origin{}}})
	assert.Error(t, err)
}

func TestAssistantStoredMeasurement(t *testing.T) {
	rt := New(WithRunStore(storeinmem.New()))
	transcript := []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "thought"}, model.ThinkingPart{Text: "reason", Signature: "opaque"}}}}
	out, err := rt.storePlanActivityOutput(t.Context(), "root", &PlanActivityOutput{TranscriptSize: contextmeasure.MessageSize{Bytes: 999, NonText: 999}}, transcript, nil)
	require.NoError(t, err)
	assert.Equal(t, contextmeasure.MessageSize{Bytes: 13, Role: model.ConversationRoleAssistant}, out.TranscriptSize)
	base := &workflowPlanInput{}
	base.adopt(out)
	require.NoError(t, base.recordAssistant(out.TranscriptRef, nil))
	require.Len(t, base.Entries, 1)
	assert.Equal(t, 13, base.Entries[0].Bytes)
	assert.Zero(t, base.Entries[0].NonText)
	assert.Equal(t, model.ConversationRoleAssistant, base.Entries[0].Role)
}
