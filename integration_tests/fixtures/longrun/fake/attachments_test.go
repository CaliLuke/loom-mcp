package fake_test

import (
	"testing"

	"example.com/longrun/env"
	"example.com/longrun/fake"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelsReadPNGAndPDFFromRequestBytes(t *testing.T) {
	png, err := env.RenderAsset("png", "IMG-12345")
	require.NoError(t, err)
	pdf, err := env.RenderAsset("pdf", "PDF-ABCDE")
	require.NoError(t, err)
	req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{
		model.ImagePart{Format: "png", Bytes: png},
		model.DocumentPart{Format: "pdf", Name: "manifest", Bytes: pdf},
		model.TextPart{Text: `NEED:{"tool":"ops_deployer.deploy","step":"final","keys":["image","pdf"]}`},
	}}}}
	reader := &fake.Reader{}
	got, err := reader.Complete(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, got.ToolCalls, 1)
	assert.JSONEq(t, `{"step":"final","target":"IMG-12345 PDF-ABCDE"}`, string(got.ToolCalls[0].Payload))
	summariser := &fake.Summariser{}
	summary, err := summariser.Complete(t.Context(), req)
	require.NoError(t, err)
	text, err := fake.RequestText(&model.Request{Messages: []*model.Message{&summary.Content[0]}})
	require.NoError(t, err)
	assert.Contains(t, text, "image = IMG-12345")
	assert.Contains(t, text, "pdf = PDF-ABCDE")
}

func TestReaderSelectsAttachmentRead(t *testing.T) {
	for _, format := range []string{"png", "pdf"} {
		t.Run(format, func(t *testing.T) {
			key, kind := "image", "image"
			if format == "pdf" {
				key, kind = "pdf", "document"
			}
			req := &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: `NEED:{"tool":"ops_deployer.deploy","step":"final","keys":["` + key + `"]}`}}},
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "text-read", Name: string(tools.RecallContext), Input: recall.Input{Read: &recall.ReadInput{ID: "57", Run: "r1", Part: recall.Result}}}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "text-read", Content: recall.Response{Attachments: []recall.AttachmentInfo{{Part: 3, Kind: kind, Format: format}}}}}},
			}}
			reader := &fake.Reader{}
			got, err := reader.Complete(t.Context(), req)
			require.NoError(t, err)
			require.Len(t, got.ToolCalls, 1)
			assert.Equal(t, tools.RecallContext, got.ToolCalls[0].Name)
			assert.JSONEq(t, `{"read":{"id":"57","run":"r1","part":"attachment","index":3,"offset":0}}`, string(got.ToolCalls[0].Payload))
		})
	}
}

func TestReaderUsesNestedAttachmentBytes(t *testing.T) {
	for _, format := range []string{"png", "pdf"} {
		t.Run(format, func(t *testing.T) {
			data, err := env.RenderAsset(format, "IMG-12345")
			require.NoError(t, err)
			key := "image"
			var part model.ToolResultContentPart = model.ImagePart{Format: "png", Bytes: data}
			if format == "pdf" {
				key = "pdf"
				part = model.DocumentPart{Format: "pdf", Name: "manifest", Bytes: data}
			}
			req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{
				model.TextPart{Text: `NEED:{"tool":"ops_deployer.deploy","step":"final","keys":["` + key + `"]}`},
				model.ToolResultPart{ToolUseID: "attachment-read", Content: recall.Response{Attachment: &recall.AttachmentInfo{Message: "57", Part: 3}}, Parts: []model.ToolResultContentPart{part}},
			}}}}
			got, err := (&fake.Reader{}).Complete(t.Context(), req)
			require.NoError(t, err)
			require.Len(t, got.ToolCalls, 1)
			assert.JSONEq(t, `{"step":"final","target":"IMG-12345"}`, string(got.ToolCalls[0].Payload))
		})
	}
}

func TestReaderUsesFindAddressForRead(t *testing.T) {
	reader := &fake.Reader{}
	req := &model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: `NEED:{"tool":"ops_deployer.deploy","step":"final","keys":["target"]}`}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "discovery", Name: string(tools.RecallContext), Input: recall.Input{Find: &recall.FindInput{Text: "target = "}}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "discovery", Content: recall.Response{Matches: []recall.Match{{ID: "m1", Run: "r1", Part: recall.Result, Offset: 7}}}}}},
	}}
	got, err := reader.Complete(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, got.ToolCalls, 1)
	assert.Equal(t, tools.RecallContext, got.ToolCalls[0].Name)
	assert.JSONEq(t, `{"read":{"id":"m1","run":"r1","part":"result","offset":7}}`, string(got.ToolCalls[0].Payload))
}
