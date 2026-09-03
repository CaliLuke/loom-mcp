package bedrock

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
)

type conformanceRuntimeClient struct {
	converse       func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error)
	lastConverse   *bedrockruntime.ConverseInput
	countTokensErr error
}

type conformanceStreamOutput struct {
	stream *bedrockruntime.ConverseStreamEventStream
}

type conformanceStreamReader struct {
	events    chan brtypes.ConverseStreamOutput
	errMu     sync.Mutex
	err       error
	closeErr  error
	closeOnce sync.Once
}

func (c *conformanceRuntimeClient) Converse(ctx context.Context, input *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	c.lastConverse = input
	if c.converse == nil {
		return &bedrockruntime.ConverseOutput{}, nil
	}
	return c.converse(ctx, input)
}

func (c *conformanceRuntimeClient) ConverseStream(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	return nil, errors.New("unexpected direct ConverseStream call")
}

func (c *conformanceRuntimeClient) CountTokens(context.Context, *bedrockruntime.CountTokensInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.CountTokensOutput, error) {
	return &bedrockruntime.CountTokensOutput{InputTokens: aws.Int32(42)}, c.countTokensErr
}

func (o *conformanceStreamOutput) GetStream() *bedrockruntime.ConverseStreamEventStream {
	return o.stream
}

func (r *conformanceStreamReader) Events() <-chan brtypes.ConverseStreamOutput {
	return r.events
}

func (r *conformanceStreamReader) Close() error {
	r.closeOnce.Do(func() {
		if r.closeErr != nil {
			r.errMu.Lock()
			r.err = r.closeErr
			r.errMu.Unlock()
		}
	})
	return r.closeErr
}

func (r *conformanceStreamReader) Err() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.err
}

func TestClientConformance(t *testing.T) {
	request := func() *model.Request {
		return &model.Request{Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}}}
	}
	newClient := func(runtime *conformanceRuntimeClient) *Client {
		return &Client{
			runtime:      runtime,
			defaultModel: "bedrock-sonnet",
			highModel:    "bedrock-opus",
			maxTok:       128,
			think:        defaultThinkingBudget,
			logger:       telemetry.NewNoopLogger(),
		}
	}
	streamOutput := func(events []brtypes.ConverseStreamOutput, streamErr error) StreamOutput {
		ch := make(chan brtypes.ConverseStreamOutput, len(events))
		for _, event := range events {
			ch <- event
		}
		close(ch)
		reader := &conformanceStreamReader{events: ch, err: streamErr}
		return newConformanceStreamOutput(reader)
	}

	testutil.RunProviderConformance(t, testutil.ProviderConformanceSuite{
		Provider: "bedrock",
		OrdinaryProviderError: func(t *testing.T) {
			providerErr := errors.New("provider unavailable")
			runtime := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return nil, providerErr
			}}
			response, err := newClient(runtime).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorIs(t, err, providerErr)
			require.ErrorContains(t, err, "bedrock unknown (converse)")
		},
		RateLimit: func(t *testing.T) {
			providerErr := &smithy.GenericAPIError{Code: bedrockThrottlingCode, Message: "slow down"}
			runtime := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return nil, providerErr
			}}
			response, err := newClient(runtime).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorIs(t, err, model.ErrRateLimited)
			var apiErr smithy.APIError
			require.ErrorAs(t, err, &apiErr)
		},
		MalformedToolCall: func(t *testing.T) {
			runtime := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return toolUseResponse(map[string]any{"": "invalid"}), nil
			}}
			response, err := newClient(runtime).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorContains(t, err, "bedrock: marshal tool input")
		},
		Cancellation: func(t *testing.T) {
			runtime := &conformanceRuntimeClient{converse: func(ctx context.Context, _ *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return nil, ctx.Err()
			}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			response, err := newClient(runtime).Complete(ctx, request())
			require.Nil(t, response)
			require.ErrorIs(t, err, context.Canceled)
		},
		StructuredOutputAndToolChoice: func(t *testing.T) {
			runtime := &conformanceRuntimeClient{}
			client := newClient(runtime)
			req := request()
			req.StructuredOutput = &model.StructuredOutput{Name: "draft", Schema: []byte(`{"type":"object"}`)}
			_, err := client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, runtime.lastConverse.OutputConfig)

			req.StructuredOutput = nil
			req.Tools = []*model.ToolDefinition{{Name: "lookup", Description: "Search", InputSchema: map[string]any{"type": "object"}}}
			req.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "lookup"}
			_, err = client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, runtime.lastConverse.ToolConfig)
			require.NotNil(t, runtime.lastConverse.ToolConfig.ToolChoice)
		},
		UsageAccounting: func(t *testing.T) {
			runtime := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return &bedrockruntime.ConverseOutput{Usage: &brtypes.TokenUsage{
					InputTokens:           aws.Int32(10),
					OutputTokens:          aws.Int32(5),
					TotalTokens:           aws.Int32(22),
					CacheReadInputTokens:  aws.Int32(3),
					CacheWriteInputTokens: aws.Int32(4),
				}}, nil
			}}
			req := request()
			req.ModelClass = model.ModelClassHighReasoning
			response, err := newClient(runtime).Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, model.TokenUsage{
				Model:            "bedrock-opus",
				ModelClass:       model.ModelClassHighReasoning,
				InputTokens:      10,
				OutputTokens:     5,
				TotalTokens:      22,
				CacheReadTokens:  3,
				CacheWriteTokens: 4,
			}, response.Usage)
		},
		OutputLimited: func(t *testing.T) {
			runtime := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return &bedrockruntime.ConverseOutput{StopReason: brtypes.StopReasonMaxTokens}, nil
			}}
			provider := newClient(runtime)
			response, err := provider.Complete(context.Background(), request())
			require.NoError(t, err)
			require.True(t, response.OutputLimited)
			client, err := model.NewClient(provider)
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), request())
			requireOutputLimitedRejection(t, err)
		},
		MultimodalInput: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			runtime := &conformanceRuntimeClient{}
			req := request()
			req.Messages[0].Parts = append(req.Messages[0].Parts, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")})
			_, err := newClient(runtime).Complete(context.Background(), req)
			require.NoError(t, err)
			require.Len(t, runtime.lastConverse.Messages, 1)
			require.Len(t, runtime.lastConverse.Messages[0].Content, 2)
			_, ok := runtime.lastConverse.Messages[0].Content[1].(*brtypes.ContentBlockMemberImage)
			require.True(t, ok)
		}},
		TypedThinking: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			runtime := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return &bedrockruntime.ConverseOutput{Output: &brtypes.ConverseOutputMemberMessage{Value: brtypes.Message{Content: []brtypes.ContentBlock{
					&brtypes.ContentBlockMemberReasoningContent{Value: &brtypes.ReasoningContentBlockMemberReasoningText{Value: brtypes.ReasoningTextBlock{
						Text: aws.String("private reasoning"), Signature: aws.String("sig"),
					}}},
				}}}}, nil
			}}
			response, err := newClient(runtime).Complete(context.Background(), request())
			require.NoError(t, err)
			require.Len(t, response.Content, 1)
			thinking, ok := response.Content[0].Parts[0].(model.ThinkingPart)
			require.True(t, ok)
			require.Equal(t, "private reasoning", thinking.Text)
			require.Equal(t, "sig", thinking.Signature)
			require.True(t, thinking.Final)
		}},
		ExactTokenCounting: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			client := newClient(&conformanceRuntimeClient{})
			counter, ok := any(client).(model.TokenCounter)
			require.True(t, ok)
			count, err := counter.CountTokens(context.Background(), request())
			require.NoError(t, err)
			require.Equal(t, 42, count.InputTokens)
			require.True(t, count.Exact)
		}},
		ToolNameRoundTrip: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			const canonical = "catalog.lookup"
			runtime := &conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return conformanceToolUseResponse(SanitizeToolName(canonical), map[string]any{"query": "docs"}), nil
			}}
			req := request()
			req.Tools = []*model.ToolDefinition{{Name: canonical, Description: "Search", InputSchema: map[string]any{"type": "object"}}}
			response, err := newClient(runtime).Complete(context.Background(), req)
			require.NoError(t, err)
			require.Len(t, response.ToolCalls, 1)
			require.Equal(t, canonical, response.ToolCalls[0].Name.String())
		}},
		Streaming: testutil.ProviderStreamingConformance{
			SetupError: func(t *testing.T) {
				providerErr := errors.New("stream setup failed")
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return nil, providerErr
				}
				stream, err := client.Stream(context.Background(), request())
				require.Nil(t, stream)
				require.ErrorIs(t, err, providerErr)
			},
			ReceiveError: func(t *testing.T) {
				providerErr := errors.New("stream receive failed")
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return streamOutput(nil, providerErr), nil
				}
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.ErrorIs(t, stream.Close(), providerErr) })
				_, err = stream.Recv()
				require.ErrorIs(t, err, providerErr)
			},
			ReceiveRateLimit: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
				providerErr := &smithy.GenericAPIError{Code: bedrockThrottlingCode, Message: "slow down"}
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return streamOutput(nil, providerErr), nil
				}
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.Error(t, stream.Close()) })
				_, err = stream.Recv()
				require.ErrorIs(t, err, model.ErrRateLimited)
				var apiErr smithy.APIError
				require.ErrorAs(t, err, &apiErr)
			}},
			StateMachine: func(t *testing.T) {
				thinkingIndex := int32(0)
				textIndex := int32(1)
				toolIndex := int32(2)
				events := []brtypes.ConverseStreamOutput{
					bedrockMessageStart(),
					bedrockReasoningDelta(thinkingIndex, &brtypes.ReasoningContentBlockDeltaMemberText{Value: "consider"}),
					bedrockReasoningDelta(thinkingIndex, &brtypes.ReasoningContentBlockDeltaMemberSignature{Value: "sig"}),
					&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: &thinkingIndex}},
					bedrockTextDelta(textIndex, "answer"),
					bedrockTextDelta(textIndex, " follows"),
					&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: &textIndex}},
					&brtypes.ConverseStreamOutputMemberContentBlockStart{Value: brtypes.ContentBlockStartEvent{
						ContentBlockIndex: &toolIndex,
						Start: &brtypes.ContentBlockStartMemberToolUse{Value: brtypes.ToolUseBlockStart{
							Name: aws.String("lookup"), ToolUseId: aws.String("call-1"),
						}},
					}},
					bedrockToolDelta(toolIndex, `{"query":`),
					bedrockToolDelta(toolIndex, `"docs"}`),
					&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: &toolIndex}},
					&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonToolUse}},
					bedrockMetadata(&brtypes.TokenUsage{InputTokens: aws.Int32(2), OutputTokens: aws.Int32(3), TotalTokens: aws.Int32(5)}),
				}
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return streamOutput(events, nil), nil
				}
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				chunks := testutil.CollectStreamChunks(t, stream)
				require.Equal(t, []string{
					model.ChunkTypeThinking, model.ChunkTypeThinking, model.ChunkTypeText,
					model.ChunkTypeText, model.ChunkTypeToolCallDelta, model.ChunkTypeToolCallDelta,
					model.ChunkTypeToolCall, model.ChunkTypeUsage, model.ChunkTypeStop,
				}, chunkTypes(chunks))
				require.Equal(t, 1, chunks[2].(model.TextChunk).Message.Meta["content_index"])
				for _, chunk := range chunks[4:6] {
					delta := chunk.(model.ToolCallDeltaChunk).Delta
					require.Equal(t, "call-1", delta.ID)
					require.Equal(t, "lookup", delta.Name.String())
				}
				call := chunks[6].(model.ToolCallChunk).ToolCall
				require.Equal(t, "call-1", call.ID)
				require.Equal(t, "lookup", call.Name.String())
				require.JSONEq(t, `{"query":"docs"}`, string(call.Payload))
				finalThinking, ok := chunks[1].(model.ThinkingChunk).Message.Parts[0].(model.ThinkingPart)
				require.True(t, ok)
				require.True(t, finalThinking.Final)
				require.Equal(t, "sig", finalThinking.Signature)
				response := stream.Response()
				require.Equal(t, string(brtypes.StopReasonToolUse), response.StopReason)
				require.Equal(t, 5, response.Usage.TotalTokens)
				require.Len(t, response.ToolCalls, 1)
				require.Len(t, response.Content, 3, "provisional thinking must be replaced by its final block")
			},
			EarlyEOF: func(t *testing.T) {
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return streamOutput([]brtypes.ConverseStreamOutput{
						bedrockMessageStart(),
						bedrockTextDelta(0, "partial"),
					}, nil), nil
				}
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { _ = stream.Close() })
				chunk, err := stream.Recv()
				require.NoError(t, err)
				require.IsType(t, model.TextChunk{}, chunk)
				_, err = stream.Recv()
				require.EqualError(t, err, "bedrock: stream ended before message_stop")
			},
			PartialCancel: func(t *testing.T) {
				events := make(chan brtypes.ConverseStreamOutput, 2)
				events <- bedrockMessageStart()
				events <- bedrockTextDelta(0, "partial")
				reader := &conformanceStreamReader{events: events}
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return newConformanceStreamOutput(reader), nil
				}
				ctx, cancel := context.WithCancel(context.Background())
				stream, err := client.Stream(ctx, request())
				require.NoError(t, err)
				chunk, err := stream.Recv()
				require.NoError(t, err)
				require.IsType(t, model.TextChunk{}, chunk)
				cancel()
				_, err = stream.Recv()
				require.ErrorIs(t, err, context.Canceled)
				require.NoError(t, stream.Close())
			},
			CloseError: func(t *testing.T) {
				closeErr := errors.New("stream close failed")
				reader := &conformanceStreamReader{events: make(chan brtypes.ConverseStreamOutput), closeErr: closeErr}
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return newConformanceStreamOutput(reader), nil
				}
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				require.ErrorIs(t, stream.Close(), closeErr)
			},
			Terminal: func(t *testing.T) {
				events := []brtypes.ConverseStreamOutput{
					bedrockMessageStart(),
					&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn}},
					bedrockMetadata(&brtypes.TokenUsage{
						InputTokens:           aws.Int32(10),
						OutputTokens:          aws.Int32(5),
						TotalTokens:           aws.Int32(22),
						CacheReadInputTokens:  aws.Int32(3),
						CacheWriteInputTokens: aws.Int32(4),
					}),
				}
				client := newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return streamOutput(events, nil), nil
				}
				req := request()
				req.ModelClass = model.ModelClassHighReasoning
				stream, err := client.Stream(context.Background(), req)
				require.NoError(t, err)

				usage, err := stream.Recv()
				require.NoError(t, err)
				usageValue := usage.(model.UsageChunk).Usage
				require.Equal(t, model.ModelClassHighReasoning, usageValue.ModelClass)
				require.Equal(t, "bedrock-opus", usageValue.Model)
				stop, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, "end_turn", stop.(model.StopChunk).Reason)
				_, err = stream.Recv()
				require.ErrorIs(t, err, io.EOF)
				require.NoError(t, stream.Close())

				client = newClient(&conformanceRuntimeClient{})
				client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return streamOutput([]brtypes.ConverseStreamOutput{
						bedrockMessageStart(),
						&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn}},
					}, nil), nil
				}
				stream, err = client.Stream(context.Background(), request())
				require.NoError(t, err)
				_, err = stream.Recv()
				require.EqualError(t, err, "bedrock: stream ended before metadata")
				require.NoError(t, stream.Close())
			},
			OutputLimited: func(t *testing.T) {
				index := int32(0)
				events := []brtypes.ConverseStreamOutput{
					bedrockMessageStart(),
					bedrockTextDelta(index, `{"partial":`),
					&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: &index}},
					&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonMaxTokens}},
					bedrockMetadata(&brtypes.TokenUsage{InputTokens: aws.Int32(1), OutputTokens: aws.Int32(1), TotalTokens: aws.Int32(2)}),
				}
				provider := newClient(&conformanceRuntimeClient{})
				provider.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
					return streamOutput(events, nil), nil
				}
				req := request()
				req.StructuredOutput = &model.StructuredOutput{Name: "result", Schema: []byte(`{"type":"object"}`)}
				stream, err := provider.Stream(context.Background(), req)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				chunks := testutil.CollectStreamChunks(t, stream)
				require.Equal(t, []string{model.ChunkTypeCompletionDelta, model.ChunkTypeUsage, model.ChunkTypeStop}, chunkTypes(chunks))
				require.True(t, chunks[len(chunks)-1].(model.StopChunk).OutputLimited)
			},
		},
	})
}

func requireOutputLimitedRejection(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var validationErr *model.OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, model.OutputValidationOutputBounds, validationErr.Kind())
}

func newConformanceStreamOutput(reader *conformanceStreamReader) StreamOutput {
	stream := bedrockruntime.NewConverseStreamEventStream(func(eventStream *bedrockruntime.ConverseStreamEventStream) {
		eventStream.Reader = reader
	})
	return &conformanceStreamOutput{stream: stream}
}

func bedrockTextDelta(index int32, text string) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
		ContentBlockIndex: &index,
		Delta:             &brtypes.ContentBlockDeltaMemberText{Value: text},
	}}
}

func bedrockMessageStart() brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberMessageStart{Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant}}
}

func bedrockMetadata(usage *brtypes.TokenUsage) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberMetadata{Value: brtypes.ConverseStreamMetadataEvent{
		Metrics: &brtypes.ConverseStreamMetrics{LatencyMs: aws.Int64(1)},
		Usage:   usage,
	}}
}

func bedrockToolDelta(index int32, fragment string) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
		ContentBlockIndex: &index,
		Delta:             &brtypes.ContentBlockDeltaMemberToolUse{Value: brtypes.ToolUseBlockDelta{Input: aws.String(fragment)}},
	}}
}

func bedrockReasoningDelta(index int32, delta brtypes.ReasoningContentBlockDelta) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
		ContentBlockIndex: &index,
		Delta:             &brtypes.ContentBlockDeltaMemberReasoningContent{Value: delta},
	}}
}

func chunkTypes(chunks []model.Chunk) []string {
	types := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		types = append(types, chunk.Kind())
	}
	return types
}

func conformanceToolUseResponse(name string, input any) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{Output: &brtypes.ConverseOutputMemberMessage{Value: brtypes.Message{Content: []brtypes.ContentBlock{
		&brtypes.ContentBlockMemberToolUse{Value: brtypes.ToolUseBlock{
			Name: aws.String(name), ToolUseId: aws.String("call-1"), Input: document.NewLazyDocument(&input),
		}},
	}}}}
}
