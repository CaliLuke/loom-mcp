package bedrock

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockStreamRetainsCitationLocationsAndSourceContent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location brtypes.CitationLocation
		want     model.CitationLocation
	}{
		{"character", &brtypes.CitationLocationMemberDocumentChar{Value: brtypes.DocumentCharLocation{DocumentIndex: aws.Int32(2), Start: aws.Int32(4), End: aws.Int32(8)}}, model.CitationLocation{DocumentChar: &model.DocumentCharLocation{DocumentIndex: 2, Start: 4, End: 8}}},
		{"chunk", &brtypes.CitationLocationMemberDocumentChunk{Value: brtypes.DocumentChunkLocation{DocumentIndex: aws.Int32(2), Start: aws.Int32(4), End: aws.Int32(8)}}, model.CitationLocation{DocumentChunk: &model.DocumentChunkLocation{DocumentIndex: 2, Start: 4, End: 8}}},
		{"page", &brtypes.CitationLocationMemberDocumentPage{Value: brtypes.DocumentPageLocation{DocumentIndex: aws.Int32(2), Start: aws.Int32(4), End: aws.Int32(8)}}, model.CitationLocation{DocumentPage: &model.DocumentPageLocation{DocumentIndex: 2, Start: 4, End: 8}}},
		{"missing coordinates", &brtypes.CitationLocationMemberDocumentPage{Value: brtypes.DocumentPageLocation{}}, model.CitationLocation{DocumentPage: &model.DocumentPageLocation{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks, response := collectBedrockBoundaryStream(t, []brtypes.ConverseStreamOutput{
				bedrockTextDelta(0, "According to the source"),
				&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &brtypes.ContentBlockDeltaMemberCitation{Value: brtypes.CitationsDelta{Title: aws.String("Evidence"), Source: aws.String("doc://record"), Location: tc.location, SourceContent: []brtypes.CitationSourceContentDelta{{Text: aws.String("quoted")}, {}, {Text: aws.String("")}}}}}},
			})
			expected := model.Citation{Title: "Evidence", Source: "doc://record", Location: tc.want, SourceContent: []string{"quoted"}}
			require.Len(t, chunks, 3)
			citation, ok := chunks[1].(model.TextChunk)
			require.True(t, ok)
			require.Equal(t, []model.Part{model.CitationsPart{Citations: []model.Citation{expected}}}, citation.Message.Parts)
			require.Len(t, response.Content, 2)
			assert.Equal(t, []model.Part{model.TextPart{Text: "According to the source"}}, response.Content[0].Parts)
			assert.Equal(t, []model.Part{model.CitationsPart{Citations: []model.Citation{expected}}}, response.Content[1].Parts)
		})
	}
}

func TestBedrockStreamPreservesOpaqueReasoningAndIgnoresEmptyDeltas(t *testing.T) {
	chunks, response := collectBedrockBoundaryStream(t, []brtypes.ConverseStreamOutput{
		bedrockTextDelta(0, ""),
		&brtypes.ConverseStreamOutputMemberContentBlockStart{Value: brtypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(0)}},
		&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &brtypes.ContentBlockDeltaMemberCitation{Value: brtypes.CitationsDelta{SourceContent: []brtypes.CitationSourceContentDelta{{}}}}}},
		bedrockReasoningDelta(0, &brtypes.ReasoningContentBlockDeltaMemberText{Value: ""}),
		bedrockReasoningDelta(0, &brtypes.ReasoningContentBlockDeltaMemberRedactedContent{Value: []byte{1, 2}}),
		bedrockReasoningDelta(0, &brtypes.ReasoningContentBlockDeltaMemberRedactedContent{Value: []byte{3}}),
		&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
		bedrockTextDelta(1, "answer"),
	})
	require.Len(t, chunks, 3)
	thinking, ok := chunks[0].(model.ThinkingChunk)
	require.True(t, ok)
	require.Len(t, thinking.Message.Parts, 1)
	part := thinking.Message.Parts[0].(model.ThinkingPart)
	assert.Equal(t, []byte{1, 2, 3}, part.Redacted)
	assert.Empty(t, part.Text)
	assert.True(t, part.Final)
	require.Len(t, response.Content, 2)
	assert.Equal(t, []model.Part{part}, response.Content[0].Parts)
	assert.Equal(t, []model.Part{model.TextPart{Text: "answer"}}, response.Content[1].Parts)
}

// collectBedrockBoundaryStream consumes the public stream through terminal metadata.
func collectBedrockBoundaryStream(t *testing.T, body []brtypes.ConverseStreamOutput) ([]model.Chunk, *model.Response) {
	t.Helper()
	events := make(chan brtypes.ConverseStreamOutput, len(body)+3)
	events <- bedrockMessageStart()
	for _, event := range body {
		events <- event
	}
	events <- bedrockMetadata(nil)
	events <- &brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn}}
	close(events)
	client := &Client{runtime: &conformanceRuntimeClient{}, defaultModel: "anthropic.claude-sonnet-4-20250514-v1:0", maxTok: 128, logger: telemetry.NewNoopLogger()}
	client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
		return newConformanceStreamOutput(&conformanceStreamReader{events: events}), nil
	}
	stream, err := client.Stream(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, stream.Close())
	})
	var chunks []model.Chunk
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}
	require.NotNil(t, stream.Response())
	return chunks, stream.Response()
}
