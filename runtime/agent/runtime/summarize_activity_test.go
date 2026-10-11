package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summaryTestClient struct {
	store          runstore.Store
	limits         model.ContextLimits
	response       *model.Response
	errors         []error
	requests       []*model.Request
	probes         []*model.Request
	partialOnError bool
}

const summaryTestText = "# Goal\nRemember the fact [message 1].\n# Decisions\nNone.\n# Facts\nFact.\n# Progress\nDone.\n# Open questions\nNone.\n# Next\nContinue."

func TestSummarizeActivityStoresRecordBeforeReference(t *testing.T) {
	rt, input, client, entries := summaryFixture(t)
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 2), input)
	require.NoError(t, err)
	require.NotNil(t, out.Checkpoint)
	assert.Empty(t, out.Kind)
	assert.Equal(t, input.From, out.From)
	assert.Equal(t, input.To, out.To)
	body, err := rt.RunStore.Get(t.Context(), "root", *out.Checkpoint)
	require.NoError(t, err)
	var record index.CheckpointRecord
	require.NoError(t, json.Unmarshal(body, &record))
	assert.Equal(t, input.To-1, record.Through)
	assert.Equal(t, entries[record.Through-1].Chain, record.Chain)
	assert.Equal(t, out.CallKeys, record.Calls)
	assert.Equal(t, "run", record.SummaryRun)
	require.Len(t, record.CitedUsers, 1)
	assert.Equal(t, "canonical user fact", record.CitedUsers[0].Text)
	assert.NotEqual(t, "Remember the fact", record.CitedUsers[0].Text)
	require.NotNil(t, record.Prompt)
	assert.Equal(t, prompt.Ident("agent.context.summary"), record.Prompt.ID)
	calls, err := rt.RunStore.ListCalls(t.Context(), "root", "run")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	start, err := callrecord.DecodeStarted(calls[0].Started)
	require.NoError(t, err)
	assert.Equal(t, callrecord.ActivitySummarize, start.Activity)
	assert.Equal(t, 9, start.Step)
	assert.Equal(t, 2, calls[0].Key.Attempt)
	assert.Nil(t, start.Request.View)
	assert.Equal(t, []prompt.PromptRef{*record.Prompt}, start.Prompts)
	rebuilt, err := callrecord.Rebuild(t.Context(), rt.RunStore, "root", start, nil)
	require.NoError(t, err)
	assert.Equal(t, client.requests[0].Messages, rebuilt.Messages)
	assert.Len(t, out.Facts, 1)
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "root", h)
	}
	in, err := checkpoint.FromRecord(t.Context(), source, *out.Checkpoint, entries, 0)
	require.NoError(t, err)
	assert.Equal(t, checkpoint.SelectPins(in.Users, input.Budget, checkpoint.DefaultLimits), out.Pinned)
	in.Pinned = out.Pinned
	message, err := checkpoint.Render(in, input.Budget, checkpoint.DefaultLimits, nil)
	require.NoError(t, err)
	assert.Contains(t, message.Parts[0].(model.TextPart).Text, summaryTestText)
}

func TestSummarizeActivityRejectedResponseRecorded(t *testing.T) {
	for _, test := range []struct {
		kind, text, stop string
		limited          bool
	}{
		{kind: "empty"},
		{kind: "truncated", text: summaryTestText, limited: true},
		{kind: "truncated", text: summaryTestText, stop: "max_tokens"},
		{kind: "too_large", text: strings.Repeat("x", 3000)},
		{kind: "not_smaller", text: strings.Repeat("x", 1900)},
		{kind: "cites_unknown", text: strings.Replace(summaryTestText, "[message 1]", "[r2 message 1]", 1)},
		{kind: "cites_not_behind", text: strings.Replace(summaryTestText, "[message 1]", "[message 5]", 1)},
		{kind: "cites_non_user", text: strings.Replace(summaryTestText, "[message 1]", "[message 2]", 1)},
		{kind: "missing_headings", text: "short fact"},
		{kind: "names_internal_tool", text: summaryTestText + " recall_context"},
	} {
		t.Run(test.kind+test.stop, func(t *testing.T) {
			rt, input, client, entries := summaryFixture(t)
			if test.kind == "not_smaller" {
				input.ViewRef.ClearedThrough = 3
			}
			client.response = summaryResponse(test.text)
			client.response.OutputLimited = test.limited
			if test.stop != "" {
				client.response.StopReason = test.stop
			}
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), input)
			require.NoError(t, err)
			assert.Equal(t, test.kind, out.Kind)
			assert.Nil(t, out.Checkpoint)
			require.Len(t, out.CallKeys, 1)
			record, err := summaryTestCall(t.Context(), rt.RunStore, "root", out.CallKeys[0])
			require.NoError(t, err)
			completion, err := callrecord.DecodeCompletion(record.Completion)
			require.NoError(t, err)
			require.NotNil(t, completion.Response)
			data, err := rt.RunStore.Get(t.Context(), "root", *completion.Response)
			require.NoError(t, err)
			var response model.Response
			require.NoError(t, json.Unmarshal(data, &response))
			assert.Equal(t, summaryResponseText(client.response), summaryResponseText(&response))
			assert.Equal(t, client.response.OutputLimited, response.OutputLimited)
			assert.Equal(t, client.response.Usage, response.Usage)
			assert.Equal(t, client.response.StopReason, response.StopReason)
			assert.Nil(t, input.ViewRef.Checkpoint)
			assert.Equal(t, index.MessageID(1), input.ViewRef.TailFrom)
			view := input.ViewRef
			view.TailFrom = input.To
			hash, err := receiptRecord(t.Context(), rt.RunStore, "root", func(ctx context.Context, h runstore.Hash) ([]byte, error) {
				return rt.RunStore.Get(ctx, "root", h)
			}, entries, view)
			require.NoError(t, err)
			data, err = rt.RunStore.Get(t.Context(), "root", hash)
			require.NoError(t, err)
			var receipt index.CheckpointRecord
			require.NoError(t, json.Unmarshal(data, &receipt))
			assert.Nil(t, receipt.Summary)
			assert.Empty(t, receipt.Calls)
			assert.Equal(t, input.To-1, receipt.Through)
			event := hooks.NewContextCompactionEvent("run", "agent", "", 9, hooks.ContextCompactionSkipped, "rejected", "", 0, 1, 0, 0, nil, time.Unix(2, 0))
			event.CallKeys = out.CallKeys
			hook, err := hooks.EncodeToHookInput(event, "")
			require.NoError(t, err)
			// Phase 3 will add the typed Kind field. The existing canonical append keeps
			// this exact payload, and RunRecord already adopts its all-started keys.
			var payload map[string]any
			require.NoError(t, json.Unmarshal(hook.Payload, &payload))
			payload["kind"] = out.Kind
			hook.Payload, err = json.Marshal(payload)
			require.NoError(t, err)
			require.NoError(t, rt.hookActivity(t.Context(), hook))
			adopted, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			require.Len(t, adopted.Runs[0].Calls, 1)
			assert.Equal(t, runrecord.Adopted, adopted.Runs[0].Calls[0].Class)
			assert.Empty(t, adopted.Runs[0].Entries)
			var stored map[string]any
			require.NoError(t, json.Unmarshal(adopted.Runs[0].Events[0].Payload, &stored))
			assert.Equal(t, out.Kind, stored["kind"])
		})
	}
}

func TestSummarizeActivityFailurePolicy(t *testing.T) {
	transient := model.NewProviderError("test", "complete", 503, model.ProviderErrorKindUnavailable, "", "busy", "", true, nil)
	wrappedTimeout := model.NewProviderError("test", "complete", 0, model.ProviderErrorKindUnknown, "", "timeout", "", true, context.DeadlineExceeded)
	rateLimit := model.NewProviderError("test", "complete", 429, model.ProviderErrorKindRateLimited, "", "slow down", "", true, nil)
	overflow := model.NewProviderError("test", "complete", 400, model.ProviderErrorKindContextOverflow, "", "full", "", false, nil)
	for _, test := range []struct {
		name     string
		failures []error
		kind     string
		calls    int
	}{
		{"retry_success", []error{transient, context.DeadlineExceeded}, "", 3},
		{"rate_limit", []error{rateLimit}, "", 2},
		{"wrapped_timeout", []error{wrappedTimeout}, "", 2},
		{"exhausted", []error{transient, transient, transient}, "summariser_error", 3},
		{"overflow", []error{overflow}, "summariser_overflow", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			client.errors = test.failures
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
			require.NoError(t, err)
			assert.Equal(t, test.kind, out.Kind)
			require.Len(t, out.CallKeys, test.calls)
			for i, key := range out.CallKeys {
				assert.Equal(t, i+1, key.Seq)
			}
			if test.kind == "" {
				data, err := rt.RunStore.Get(t.Context(), "root", *out.Checkpoint)
				require.NoError(t, err)
				var record index.CheckpointRecord
				require.NoError(t, json.Unmarshal(data, &record))
				assert.Equal(t, out.CallKeys[len(out.CallKeys)-1:], record.Calls)
			}
			eventType := hooks.ContextCompactionSkipped
			if test.kind == "" {
				eventType = hooks.ContextCompacted
			}
			event := hooks.NewContextCompactionEvent("run", "agent", "", 9, eventType, test.kind, "summary", 1, 1, 100, 50, out.Checkpoint, time.Unix(1, 0))
			event.CallKeys = out.CallKeys
			hook, err := hooks.EncodeToHookInput(event, "")
			require.NoError(t, err)
			require.NoError(t, rt.hookActivity(t.Context(), hook))
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			require.Len(t, record.Runs[0].Calls, test.calls)
			for _, call := range record.Runs[0].Calls {
				assert.Equal(t, runrecord.Adopted, call.Class)
			}
		})
	}
}

func TestSummarizeActivityRecordingFaults(t *testing.T) {
	for _, window := range []string{"started", "response", "completion", "checkpoint"} {
		t.Run(window, func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			store := &summaryFaultStore{Store: rt.RunStore, window: window}
			rt.RunStore = store
			client.store = store
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
			require.Error(t, err)
			assert.Nil(t, out)
			if window == "started" {
				assert.Empty(t, client.requests)
			} else {
				assert.Len(t, client.requests, 1)
			}
		})
	}
}

func summaryFixture(t *testing.T) (*Runtime, *SummarizeContextInput, *summaryTestClient, []index.MessageEntry) {
	t.Helper()
	rt := New()
	require.NoError(t, rt.registerSummaryPrompt("agent", SummarySettings{}))
	put := func(v any) runstore.Hash {
		data, err := json.Marshal(v)
		require.NoError(t, err)
		h, err := rt.RunStore.Put(t.Context(), "root", data)
		require.NoError(t, err)
		return h
	}
	user := put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "canonical user fact"}}})
	call := put(map[string]any{"Name": "tool", "ToolCallID": "call", "Payload": map[string]any{"query": "fact"}})
	result := put(strings.Repeat("eligible fact ", 500))
	tail := put([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "next assistant"}}}})
	nextUser := put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "next user"}}})
	seg, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: user, Bytes: 19}, {ID: 2, Kind: index.Assistant, Calls: []runstore.Hash{call}, ToolUseIDs: []string{"call"}}, {ID: 3, Kind: index.ToolResult, Hash: result, ToolUseIDs: []string{"call"}, Batch: 3, Bytes: 6500}, {ID: 4, Kind: index.Assistant, Hash: tail}, {ID: 5, Kind: index.Message, Role: model.ConversationRoleUser, Hash: nextUser}})
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	head, err := rt.RunStore.Put(t.Context(), "root", data)
	require.NoError(t, err)
	client := &summaryTestClient{store: rt.RunStore, limits: model.ContextLimits{ContextWindow: 200000}, response: summaryResponse(summaryTestText)}
	require.NoError(t, rt.RegisterModel("summary", client))
	in := &SummarizeContextInput{AgentID: "agent", RunID: "run", Scope: "root", Step: 9, ViewRef: index.ViewRef{IndexHead: head, TailFrom: 1}, From: 1, To: 4, Budget: index.RenderBudget{KeepUserTokens: 150, MaxSummaryTokens: 1000, ReceiptTokens: 200, FramingTokens: 200}, ContextModel: SummaryModel{ID: "summary"}, PromptID: "agent.context.summary"}
	return rt, in, client, seg.Entries
}

func summaryResponse(text string) *model.Response {
	parts := []model.Part{}
	if text != "" {
		parts = append(parts, model.TextPart{Text: text})
	}
	return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: parts}}, StopReason: "end_turn", Usage: model.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
}

func (c *summaryTestClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	info, ok := model.CallInfoFrom(ctx)
	if !ok {
		return nil, errors.New("missing call identity")
	}
	call, err := summaryTestCall(ctx, c.store, info.RootRunID, runstore.CallKey{RunID: info.RunID, Seq: info.Seq, Attempt: info.Attempt})
	if err != nil {
		return nil, err
	}
	if call.State != runstore.CallStarted {
		return nil, errors.New("send without started record")
	}
	c.requests = append(c.requests, req)
	if len(c.errors) > 0 {
		err := c.errors[0]
		c.errors = c.errors[1:]
		if c.partialOnError {
			return c.response, err
		}
		return nil, err
	}
	return c.response, nil
}
func (c *summaryTestClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, errors.New("unary only")
}
func (c *summaryTestClient) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	c.probes = append(c.probes, req)
	return c.limits, "provider-summary", c.limits.ContextWindow > 0
}

type summaryFaultStore struct {
	runstore.Store
	window string
}

func (s *summaryFaultStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	var fields map[string]rawjson.Message
	if err := json.Unmarshal(data, &fields); err == nil {
		if s.window == "response" && fields["StopReason"] != nil {
			return "", errors.New("response fault")
		}
		if s.window == "checkpoint" && fields["through"] != nil && fields["summary"] != nil {
			return "", errors.New("checkpoint fault")
		}
	}
	return s.Store.Put(ctx, scope, data)
}
func (s *summaryFaultStore) PutCall(ctx context.Context, scope string, call runstore.CallRecord) error {
	if s.window == "started" && call.State == runstore.CallStarted {
		return errors.New("started fault")
	}
	if s.window == "completion" && call.State != runstore.CallStarted {
		return errors.New("completion fault")
	}
	return s.Store.PutCall(ctx, scope, call)
}

func summaryTestCall(ctx context.Context, store runstore.Store, scope string, key runstore.CallKey) (runstore.CallRecord, error) {
	calls, err := store.ListCalls(ctx, scope, key.RunID)
	if err != nil {
		return runstore.CallRecord{}, err
	}
	for _, call := range calls {
		if call.Key == key {
			return call, nil
		}
	}
	return runstore.CallRecord{}, errors.New("call not found")
}
