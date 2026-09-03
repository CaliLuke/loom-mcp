package bedrock

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestChunkProcessor_MetadataUsageIncludesCacheTokens(t *testing.T) {
	var (
		inTokens   int32 = 10
		outTokens  int32 = 4
		total      int32 = 22
		cacheRead  int32 = 3
		cacheWrite int32 = 5
		latency    int64 = 1
	)

	var gotChunk model.Chunk

	cp := newChunkProcessor(
		func(ch model.Chunk) error {
			gotChunk = ch
			return nil
		},
		map[string]string{},
		"test-model-id",
		model.ModelClassDefault,
		nil,
	)

	event := &brtypes.ConverseStreamOutputMemberMetadata{
		Value: brtypes.ConverseStreamMetadataEvent{
			Metrics: &brtypes.ConverseStreamMetrics{LatencyMs: &latency},
			Usage: &brtypes.TokenUsage{
				InputTokens:           &inTokens,
				OutputTokens:          &outTokens,
				TotalTokens:           &total,
				CacheReadInputTokens:  &cacheRead,
				CacheWriteInputTokens: &cacheWrite,
			},
		},
	}

	err := cp.Handle(event)
	require.NoError(t, err)

	usage := gotChunk.(model.UsageChunk).Usage
	require.Equal(t, int(inTokens), usage.InputTokens)
	require.Equal(t, int(outTokens), usage.OutputTokens)
	require.Equal(t, int(total), usage.TotalTokens)
	require.Equal(t, int(cacheRead), usage.CacheReadTokens)
	require.Equal(t, int(cacheWrite), usage.CacheWriteTokens)
	require.Equal(t, "test-model-id", usage.Model)
	require.Equal(t, model.ModelClassDefault, usage.ModelClass)
}

func TestChunkProcessorEmitsCitationDataInStream(t *testing.T) {
	var chunks []model.Chunk
	processor := newChunkProcessor(
		func(chunk model.Chunk) error {
			chunks = append(chunks, chunk)
			return nil
		},
		nil,
		"test-model-id",
		model.ModelClassDefault,
		nil,
	)
	title := "source"
	require.NoError(t, processor.emitCitationDelta(4, &brtypes.ContentBlockDeltaMemberCitation{
		Value: brtypes.CitationsDelta{Title: &title},
	}))

	require.Len(t, chunks, 1)
	message := chunks[0].(model.TextChunk).Message
	require.Equal(t, 4, message.Meta["content_index"])
	part := message.Parts[0].(model.CitationsPart)
	require.Equal(t, []model.Citation{{Title: "source"}}, part.Citations)
}

func TestChunkProcessor_StructuredOutputEmitsCompletionDeltaAndFinalCompletion(t *testing.T) {
	idx := int32(0)
	var chunks []model.Chunk

	cp := newChunkProcessor(
		func(ch model.Chunk) error {
			chunks = append(chunks, ch)
			return nil
		},
		map[string]string{},
		"test-model-id",
		model.ModelClassDefault,
		&model.StructuredOutput{
			Name:   "draft_from_transcript",
			Schema: []byte(`{"type":"object"}`),
		},
	)

	err := cp.Handle(&brtypes.ConverseStreamOutputMemberMessageStart{})
	require.NoError(t, err)
	err = cp.Handle(&brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: &idx,
			Delta: &brtypes.ContentBlockDeltaMemberText{
				Value: `{"assistant_text":"created a draft"}`,
			},
		},
	})
	require.NoError(t, err)
	err = cp.Handle(&brtypes.ConverseStreamOutputMemberContentBlockStop{
		Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: &idx},
	})
	require.NoError(t, err)
	err = cp.Handle(&brtypes.ConverseStreamOutputMemberMessageStop{})
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	latency := int64(1)
	err = cp.Handle(&brtypes.ConverseStreamOutputMemberMetadata{Value: brtypes.ConverseStreamMetadataEvent{
		Metrics: &brtypes.ConverseStreamMetrics{LatencyMs: &latency},
		Usage:   &brtypes.TokenUsage{},
	}})
	require.NoError(t, err)

	require.Len(t, chunks, 4)
	delta := chunks[0].(model.CompletionDeltaChunk).Delta
	require.Equal(t, "draft_from_transcript", delta.Name)
	require.JSONEq(t, `{"assistant_text":"created a draft"}`, delta.Delta)

	completion := chunks[1].(model.CompletionChunk).Completion
	require.Equal(t, "draft_from_transcript", completion.Name)
	require.JSONEq(t, `{"assistant_text":"created a draft"}`, string(completion.Payload))

	require.IsType(t, model.UsageChunk{}, chunks[2])
	require.IsType(t, model.StopChunk{}, chunks[3])
}

func TestChunkProcessor_StructuredOutputRejectsInvalidFinalJSON(t *testing.T) {
	idx := int32(0)

	cp := newChunkProcessor(
		func(model.Chunk) error {
			return nil
		},
		map[string]string{},
		"test-model-id",
		model.ModelClassDefault,
		&model.StructuredOutput{
			Name:   "draft_from_transcript",
			Schema: []byte(`{"type":"object"}`),
		},
	)

	err := cp.Handle(&brtypes.ConverseStreamOutputMemberMessageStart{})
	require.NoError(t, err)
	err = cp.Handle(&brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: &idx,
			Delta: &brtypes.ContentBlockDeltaMemberText{
				Value: `{"assistant_text":"created a draft"`,
			},
		},
	})
	require.NoError(t, err)
	err = cp.Handle(&brtypes.ConverseStreamOutputMemberContentBlockStop{
		Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: &idx},
	})
	require.NoError(t, err)
	err = cp.Handle(&brtypes.ConverseStreamOutputMemberMessageStop{
		Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not valid JSON")
}
