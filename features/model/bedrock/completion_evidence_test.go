package bedrock

import (
	"context"
	"net/http"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockCompletePreservesCitationAndOpaqueReasoningEvidence(t *testing.T) {
	locations := []brtypes.CitationLocation{
		&brtypes.CitationLocationMemberDocumentChar{Value: brtypes.DocumentCharLocation{DocumentIndex: aws.Int32(2), Start: aws.Int32(4), End: aws.Int32(8)}},
		&brtypes.CitationLocationMemberDocumentChunk{Value: brtypes.DocumentChunkLocation{DocumentIndex: aws.Int32(2), Start: aws.Int32(4), End: aws.Int32(8)}},
		nil,
	}
	citations := make([]brtypes.Citation, 0, len(locations))
	for _, location := range locations {
		citations = append(citations, brtypes.Citation{Title: aws.String("Evidence"), Source: aws.String("doc://record"), Location: location})
	}
	provider := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		return &bedrockruntime.ConverseOutput{Output: &brtypes.ConverseOutputMemberMessage{Value: brtypes.Message{Role: brtypes.ConversationRoleAssistant, Content: []brtypes.ContentBlock{
			&brtypes.ContentBlockMemberText{},
			&brtypes.ContentBlockMemberReasoningContent{},
			&brtypes.ContentBlockMemberReasoningContent{Value: &brtypes.ReasoningContentBlockMemberReasoningText{}},
			&brtypes.ContentBlockMemberCitationsContent{},
			&brtypes.ContentBlockMemberReasoningContent{Value: &brtypes.ReasoningContentBlockMemberRedactedContent{Value: []byte("opaque")}},
			&brtypes.ContentBlockMemberCitationsContent{Value: brtypes.CitationsContentBlock{Content: []brtypes.CitationGeneratedContent{&brtypes.CitationGeneratedContentMemberText{Value: "answer"}}, Citations: citations}},
		}}}, StopReason: brtypes.StopReasonEndTurn}, nil
	}}
	client := &Client{runtime: provider, defaultModel: "claude", logger: telemetry.NewNoopLogger()}
	response, err := client.Complete(t.Context(), bedrockEvidenceRequest())
	require.NoError(t, err)
	require.Len(t, response.Content, 2)
	assert.Equal(t, []model.Part{model.ThinkingPart{Redacted: []byte("opaque"), Final: true}}, response.Content[0].Parts)
	assert.Equal(t, []model.Part{model.CitationsPart{Text: "answer", Citations: []model.Citation{
		{Title: "Evidence", Source: "doc://record", Location: model.CitationLocation{DocumentChar: &model.DocumentCharLocation{DocumentIndex: 2, Start: 4, End: 8}}},
		{Title: "Evidence", Source: "doc://record", Location: model.CitationLocation{DocumentChunk: &model.DocumentChunkLocation{DocumentIndex: 2, Start: 4, End: 8}}},
		{Title: "Evidence", Source: "doc://record"},
	}}}, response.Content[1].Parts)
}

func TestBedrockPublicCallsClassifyHTTPFailures(t *testing.T) {
	for _, tt := range []struct {
		status int
		kind   model.ProviderErrorKind
		retry  bool
	}{
		{401, model.ProviderErrorKindAuth, false}, {403, model.ProviderErrorKindAuth, false}, {503, model.ProviderErrorKindUnavailable, true},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			cause := &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: tt.status}}, Err: assert.AnError}
			client := &Client{runtime: &errorRuntimeClient{converseErr: cause, converseStreamErr: cause}, defaultModel: "claude", logger: telemetry.NewNoopLogger()}
			_, err := client.Complete(t.Context(), bedrockEvidenceRequest())
			pe, ok := model.AsProviderError(err)
			require.True(t, ok)
			assert.Equal(t, tt.kind, pe.Kind())
			assert.Equal(t, tt.retry, pe.Retryable())
			require.ErrorIs(t, err, cause)
			stream, err := client.Stream(t.Context(), bedrockEvidenceRequest())
			assert.Nil(t, stream)
			pe, ok = model.AsProviderError(err)
			require.True(t, ok)
			assert.Equal(t, tt.kind, pe.Kind())
			assert.Equal(t, tt.retry, pe.Retryable())
			assert.ErrorIs(t, err, cause)
		})
	}
}

func TestBedrockRejectsAbsentProviderOutputs(t *testing.T) {
	client := &Client{runtime: &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
		return nil, nil
	}}, defaultModel: "claude", logger: telemetry.NewNoopLogger()}
	response, err := client.Complete(t.Context(), bedrockEvidenceRequest())
	require.ErrorContains(t, err, "response is nil")
	assert.Nil(t, response)
	for _, output := range []StreamOutput{nil, &conformanceStreamOutput{}} {
		client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
			return output, nil
		}
		stream, err := client.Stream(t.Context(), bedrockEvidenceRequest())
		require.ErrorContains(t, err, "stream")
		assert.Nil(t, stream)
	}
}

func bedrockEvidenceRequest() *model.Request {
	return &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
}
