package runtime

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	anthropicmodel "github.com/CaliLuke/loom-mcp/v2/features/model/anthropic"
	bedrockmodel "github.com/CaliLuke/loom-mcp/v2/features/model/bedrock"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalRecallProviderBodies(t *testing.T) {
	for _, provider := range []string{"anthropic", "bedrock", "capability-less"} {
		t.Run(provider, func(t *testing.T) {
			client, body := multimodalProvider(t, provider)
			rt := New(WithEngine(inmem.New()), WithContextCompaction("content.agent", ContextCompaction{KeepRecentTokens: 100, ProtectRecentToolTokens: 2000, MinSavingTokens: 1}), WithContextLimits(map[string]model.ContextLimits{"multimodal": {ContextWindow: 12000}}))
			require.NoError(t, rt.RegisterModel("multimodal", client))
			grow := newAnyJSONSpec("test.grow", "test")
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{grow}, Inline: true, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
				return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: "tick"}), nil
			}}))
			step := 0
			decide := func(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
				step++
				client, ok := agent.ModelClient("multimodal")
				require.True(t, ok)
				_, err := client.Complete(ctx, &model.Request{Messages: messages, MaxTokens: 128})
				if err != nil {
					return nil, err
				}
				switch step {
				case 1:
					return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: grow.Name, ToolCallID: "grow", Payload: rawjson.Message(`{}`)}}}, nil
				case 2:
					assert.Contains(t, messagePartsText(messages[0]), "message 1")
					return &planner.PlanResult{ToolCalls: []planner.ToolRequest{
						{Name: tools.RecallContext, ToolCallID: "image", Payload: rawjson.Message(`{"read":{"id":"1","part":"attachment","index":2,"offset":0}}`)},
						{Name: tools.RecallContext, ToolCallID: "pdf", Payload: rawjson.Message(`{"read":{"id":"3","part":"attachment","index":2,"offset":0}}`)},
					}}, nil
				default:
					return oracleFinal(), nil
				}
			}
			registerContentAgent(t, rt, &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				return decide(ctx, in.Agent, in.Messages)
			}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				return decide(ctx, in.Agent, in.Messages)
			}})
			seed := []*model.Message{
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "image"}, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}}},
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "noted"}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "PDF"}, model.DocumentPart{Name: "manual", Format: model.DocumentFormatPDF, Bytes: []byte("%PDF-test")}}},
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("history ", 12000)}}}, contentMessage("recall both"),
			}
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", seed, WithRunID("multimodal-run"))
			require.NoError(t, err)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "multimodal-run", "multimodal-run")
			require.NoError(t, err)
			state := compaction.State{}
			for _, call := range record.Runs[0].Calls {
				if call.Completion != nil && call.Completion.Facts != nil {
					state = compaction.Observe(state, []contextmeasure.RequestFacts{*call.Completion.Facts})
				}
			}
			support := compaction.ToolResultParts(state)
			assert.Equal(t, support, record.Runs[0].ToolResultParts)
			wantParts := 1
			wantOutcome := "ok"
			if provider == "capability-less" {
				wantParts = 0
				wantOutcome = "unsupported"
				assert.Empty(t, support.ImageFormats)
			} else {
				assert.Contains(t, support.ImageFormats, model.ImageFormatPNG)
			}
			events := 0
			for _, event := range record.Runs[0].Events {
				if event.Type == hooks.ContextRecalled {
					var recalled hooks.ContextRecalledEvent
					require.NoError(t, json.Unmarshal(event.Payload, &recalled))
					assert.Equal(t, wantParts, recalled.Parts)
					assert.Equal(t, wantOutcome, recalled.Outcome)
					events++
				}
			}
			assert.Equal(t, 2, events)
			for _, entry := range record.Runs[0].Entries {
				if entry.Kind == index.ToolResult && entry.Result != nil && entry.Result.Parts != "" {
					assert.Positive(t, entry.NonText)
				}
			}
			if provider != "capability-less" {
				testutil.NewGoldenFile(t, "testdata/multimodal_recall").CompareBytes(body(), provider+".json")
			}
		})
	}
}

func multimodalProvider(t *testing.T, provider string) (model.Client, func() []byte) {
	t.Helper()
	if provider == "capability-less" {
		return &recordingTestClient{store: &recordingTestStore{}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "ok"}}}}, StopReason: "end_turn"}}, func() []byte {
			return nil
		}
	}
	var mu sync.Mutex
	var resultBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Error(err)
			return
		}
		var results []any
		messages, ok := body["messages"].([]any)
		if ok {
			for _, m := range messages {
				message := m.(map[string]any)
				content, ok := message["content"].([]any)
				if !ok {
					continue
				}
				for _, c := range content {
					part := c.(map[string]any)
					if part["type"] == "tool_result" || part["toolResult"] != nil {
						normalizeMultimodalJSONText(t, part)
						results = append(results, part)
					}
				}
			}
		}
		encoded, err := json.Marshal(results, json.Deterministic(true))
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		resultBody = encoded
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		response := `{"id":"test","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
		if provider == "bedrock" {
			response = `{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"metrics":{"latencyMs":1}}`
		}
		if _, err := io.WriteString(w, response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	var raw model.Provider
	if provider == "anthropic" {
		sdkClient := sdk.NewClient(option.WithAPIKey("test"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
		client, err := anthropicmodel.New(&sdkClient.Messages, anthropicmodel.Options{DefaultModel: "claude-sonnet-4-5", MaxTokens: 128})
		require.NoError(t, err)
		raw = client
	} else {
		sdkClient := bedrockruntime.New(bedrockruntime.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
		})})
		client, err := bedrockmodel.New(sdkClient, bedrockmodel.Options{Runtime: sdkClient, DefaultModel: "us.anthropic.claude-sonnet-4-5", MaxTokens: 128})
		require.NoError(t, err)
		raw = client
	}
	client, err := model.NewClient(raw)
	require.NoError(t, err)
	return client, func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return append([]byte(nil), resultBody...)
	}
}

// normalizeMultimodalJSONText canonicalizes JSON inside provider text blocks.
func normalizeMultimodalJSONText(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if text, ok := item.(string); ok && key == "text" {
				var decoded any
				if json.Unmarshal([]byte(text), &decoded) == nil {
					data, err := json.Marshal(decoded, json.Deterministic(true))
					require.NoError(t, err)
					value[key] = string(data)
				}
			} else {
				normalizeMultimodalJSONText(t, item)
			}
		}
	case []any:
		for _, item := range value {
			normalizeMultimodalJSONText(t, item)
		}
	}
}
