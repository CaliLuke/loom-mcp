package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summaryEligibleStore struct {
	runstore.Store
	forbidden runstore.Hash
}

type summaryCountingPromptStore struct {
	prompt.Store
	resolves int
}

func TestSummaryEligibleEvidenceP4(t *testing.T) {
	rt, in, client, entries := summaryFixture(t)
	put := func(v any) runstore.Hash {
		data, err := json.Marshal(v)
		require.NoError(t, err)
		hash, err := rt.RunStore.Put(t.Context(), "root", data)
		require.NoError(t, err)
		return hash
	}
	fact := "the middle fact is platinum"
	body := strings.Repeat("x", 2_500_000) + fact + strings.Repeat("y", 2_500_000)
	entries[2].Hash = put(body)
	entries[2].Bytes = len(body) + 2
	server := put("SERVER_ONLY_MARKER")
	entries[2].Record = []runstore.Hash{server}
	entries[2].Output = server
	entries[1].Calls = []runstore.Hash{put(map[string]any{"Name": "tool", "ToolCallID": "call", "Payload": map[string]any{"query": "middle"}, "InjectedPayload": map[string]any{"secret": "INJECT_MARKER"}}), put(map[string]any{"Name": "bookkeeping", "ToolCallID": "hidden", "Payload": map[string]any{"secret": "HIDDEN_MARKER"}})}
	entries[1].ToolUseIDs = []string{"call", "hidden"}
	entries[1].Hash = put([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("eligible assistant prose ", 100)}}}})
	// Production bookkeeping declarations precede their result batch.
	hiddenResult := index.MessageEntry{Kind: index.ToolResult, Hash: put("HIDDEN_RESULT_MARKER"), ToolUseIDs: []string{"hidden"}, Batch: 4}
	declaration := index.MessageEntry{Kind: index.Structural, ToolUseIDs: []string{"hidden"}, Structural: &index.StructuralEntry{Kind: index.HideBookkeeping, ProtocolVersion: index.TemplateProtocolVersion}}
	result := entries[2]
	result.Batch = 4
	entries = append(entries[:2], append([]index.MessageEntry{declaration, result, hiddenResult}, entries[3:]...)...)
	for i := range entries {
		entries[i].ID = index.MessageID(i + 1)
	}
	in.To = 6
	replaceSummaryEntries(t, rt, in, entries)
	store := summaryEligibleStore{Store: rt.RunStore, forbidden: server}
	rt.RunStore = store
	client.store = store
	client.limits.ContextWindow = 4_000_000
	client.response = summaryResponse(strings.Replace(summaryTestText, "Fact.", fact, 1))
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.Empty(t, out.Kind)
	require.NotNil(t, out.Checkpoint)
	requestBytes, err := callrecord.Encode(client.requests[0])
	require.NoError(t, err)
	assert.Contains(t, string(requestBytes), fact)
	for _, marker := range []string{"SERVER_ONLY_MARKER", "INJECT_MARKER", "HIDDEN_MARKER", "HIDDEN_RESULT_MARKER"} {
		assert.NotContains(t, string(requestBytes), marker)
	}
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "root", h)
	}
	committed, err := index.Walk(t.Context(), source, in.ViewRef.IndexHead)
	require.NoError(t, err)
	plannerMessages, err := index.Render(t.Context(), committed, source)
	require.NoError(t, err)
	require.NoError(t, recordSummaryAcceptanceRequest(t, rt, client, plannerMessages))
	_, err = rt.SessionStore.CreateSession(t.Context(), "session", time.Unix(1, 0))
	require.NoError(t, err)
	require.NoError(t, rt.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: "run", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning}))
	handler := recall.Handler{Store: store, Sessions: rt.SessionStore}
	response, err := handler.Handle(t.Context(), recall.Request{RunID: "run", Scope: "root", View: index.ViewRef{IndexHead: in.ViewRef.IndexHead, TailFrom: in.To, Checkpoint: out.Checkpoint}}, recall.Input{Kind: recall.Find, Find: &recall.FindInput{Text: fact}})
	require.NoError(t, err)
	require.Empty(t, response.Code)
	require.Len(t, response.Matches, 1)
	assert.Contains(t, response.Matches[0].Snippet, fact)
	data, err := json.Marshal(response)
	require.NoError(t, err)
	require.NoError(t, recordSummaryAcceptanceRequest(t, rt, client, []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: string(data)}}}}))
	calls, err := store.ListCalls(t.Context(), "root", "run")
	require.NoError(t, err)
	for _, call := range calls {
		start, err := callrecord.DecodeStarted(call.Started)
		require.NoError(t, err)
		req, err := callrecord.Rebuild(t.Context(), store, "root", start, nil)
		require.NoError(t, err)
		data, err := callrecord.Encode(req)
		require.NoError(t, err)
		for _, marker := range []string{"SERVER_ONLY_MARKER", "INJECT_MARKER", "HIDDEN_MARKER", "HIDDEN_RESULT_MARKER"} {
			assert.NotContains(t, string(data), marker)
		}
	}
}

func TestSummaryUnsummarisedRangesRecall(t *testing.T) {
	rt, in, client, entries := summaryFixture(t)
	client.limits.ContextWindow = 8000
	// Result content remains exact stored JSON, including the outer string quotes.
	text := strings.Repeat("a世", 10000)
	data, err := json.Marshal(text)
	require.NoError(t, err)
	entries[2].Hash, err = rt.RunStore.Put(t.Context(), "root", data)
	require.NoError(t, err)
	entries[2].Bytes = len(data)
	replaceSummaryEntries(t, rt, in, entries)
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.Empty(t, out.Kind)
	recordBytes, err := rt.RunStore.Get(t.Context(), "root", *out.Checkpoint)
	require.NoError(t, err)
	var record index.CheckpointRecord
	require.NoError(t, json.Unmarshal(recordBytes, &record))
	require.Len(t, record.Receipt, 1)
	require.Len(t, record.Receipt[0].Unsummarised, 1)
	span := record.Receipt[0].Unsummarised[0]
	assert.Equal(t, len(data)-1, span.Through)
	assert.Equal(t, len(data)-span.From, out.UnsummarisedBytes)
	_, err = rt.SessionStore.CreateSession(t.Context(), "session", time.Unix(1, 0))
	require.NoError(t, err)
	require.NoError(t, rt.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: "run", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning}))
	handler := recall.Handler{Store: rt.RunStore, Sessions: rt.SessionStore}
	response, err := handler.Handle(t.Context(), recall.Request{RunID: "run", Scope: "root", View: index.ViewRef{IndexHead: in.ViewRef.IndexHead, TailFrom: 4}, PageBytes: len(data)}, recall.Input{Kind: recall.Read, Read: &recall.ReadInput{ID: "call", Part: recall.Result, Offset: span.From}})
	require.NoError(t, err)
	require.Empty(t, response.Code)
	assert.Equal(t, string(data[span.From:span.Through+1]), response.Text)
}

func TestSummaryStoredWholeThenBounded(t *testing.T) {
	rt, in, client, entries := summaryFixture(t)
	text := summaryTestText + strings.Repeat(" preserved fact", 180)
	client.response = summaryResponse(text)
	state := compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "large", ContextWindow: 200000, ViewBearing: true}})
	in.Budget = compaction.Decide(state, nil, index.ViewRef{}, compaction.Settings{}).View.Budget
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.Empty(t, out.Kind)
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "root", h)
	}
	candidate, err := checkpoint.FromRecord(t.Context(), source, *out.Checkpoint, entries, 0)
	require.NoError(t, err)
	candidate.Pinned = out.Pinned
	large, err := checkpoint.Render(candidate, in.Budget, checkpoint.DefaultLimits, nil)
	require.NoError(t, err)
	assert.Contains(t, large.Parts[0].(model.TextPart).Text, text)
	state = compaction.Observe(state, []contextmeasure.RequestFacts{{Model: "small", ContextWindow: 8000, ViewBearing: true}})
	smallBudget := compaction.Decide(state, nil, index.ViewRef{}, compaction.Settings{}).View.Budget
	candidate.Pinned = checkpoint.SelectPins(candidate.Users, smallBudget, checkpoint.DefaultLimits)
	small, err := checkpoint.Render(candidate, smallBudget, checkpoint.DefaultLimits, nil)
	require.NoError(t, err)
	assert.NotContains(t, small.Parts[0].(model.TextPart).Text, text)
	assert.Contains(t, small.Parts[0].(model.TextPart).Text, "full text: checkpoint")
	assert.LessOrEqual(t, len(small.Parts[0].(model.TextPart).Text), checkpoint.MaxBytes(smallBudget))
	assert.Equal(t, text, candidate.Summary.Text)
}

func TestSummaryPromptScopeAndFoldIdentity(t *testing.T) {
	rt, in, client, entries := summaryFixture(t)
	store := prompt.NewInMemoryStore()
	counted := &summaryCountingPromptStore{Store: store}
	rt.PromptRegistry.SetStore(counted)
	require.NoError(t, store.Set(t.Context(), in.PromptID, prompt.Scope{SessionID: "selected"}, "CUSTOM {{.Instructions}} / {{.Budget}} / {{.Previous}} / {{.Evicted}}", nil))
	in.SessionID = "selected"
	in.Settings.Instructions = "retain the facts"
	// Enough assistant prose for several folds; result cap is independent.
	body, err := json.Marshal([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("fold evidence ", 1400)}}}})
	require.NoError(t, err)
	entries[1].Hash, err = rt.RunStore.Put(t.Context(), "root", body)
	require.NoError(t, err)
	replaceSummaryEntries(t, rt, in, entries)
	client.limits.ContextWindow = 8000
	client.response = summaryResponse("short custom fact")
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.Empty(t, out.Kind)
	assert.Equal(t, 1, counted.resolves)
	assert.Greater(t, out.Chunks, 1)
	require.Len(t, out.CallKeys, out.Chunks)
	for i, req := range client.requests {
		text := req.Messages[0].Parts[0].(model.TextPart).Text
		assert.Contains(t, text, "CUSTOM retain the facts / 1000")
		if i > 0 {
			assert.Contains(t, text, "> short custom fact")
		}
		assert.Equal(t, i+1, out.CallKeys[i].Seq)
		assert.Equal(t, []prompt.PromptRef{*out.Prompt}, req.PromptRefs)
	}
}

func TestSummaryPromptRegisteredWithAgent(t *testing.T) {
	for _, source := range []string{"", "custom baseline {{.Instructions}}"} {
		rt := New(WithEngine(&stubEngine{}))
		rt.contextCompaction = map[agent.Ident]ContextCompaction{"agent": {Summary: SummarySettings{Prompt: source}}}
		reg := AgentRegistration{ID: "agent", Planner: &stubPlanner{}, PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool", Workflow: engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}}
		require.NoError(t, rt.RegisterAgent(t.Context(), reg))
		spec, err := rt.PromptRegistry.Resolve(t.Context(), "agent.context.summary", prompt.Scope{})
		require.NoError(t, err)
		expected := source
		if expected == "" {
			expected = defaultContextSummaryPrompt
		}
		assert.Equal(t, expected, spec.Template)
	}
}

func TestSummaryAdoptionRequiresRecordEvent(t *testing.T) {
	rt, in, _, _ := summaryFixture(t)
	rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
		t.Fatal("compaction record intercepted")
		return nil, nil
	}}}
	first, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	second, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 2), in)
	require.NoError(t, err)
	assert.NotEqual(t, first.CallKeys, second.CallKeys)
	before, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	require.Len(t, before.Runs[0].Calls, 2)
	for _, call := range before.Runs[0].Calls {
		assert.Equal(t, runrecord.Abandoned, call.Class, "checkpoint Calls do not adopt")
	}
	event := hooks.NewContextCompactionEvent("run", "agent", "", 9, hooks.ContextCompacted, "", "summary", 1, 1, 100, 50, second.Checkpoint, time.Unix(2, 0))
	event.CallKeys = second.CallKeys
	hook, err := hooks.EncodeToHookInput(event, "")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(t.Context(), hook))
	after, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	assert.Equal(t, runrecord.Abandoned, after.Runs[0].Calls[0].Class)
	assert.Equal(t, runrecord.Adopted, after.Runs[0].Calls[1].Class)
}

func (s summaryEligibleStore) Get(ctx context.Context, scope string, h runstore.Hash) ([]byte, error) {
	if h == s.forbidden {
		return nil, errors.New("server-only content fetched")
	}
	return s.Store.Get(ctx, scope, h)
}
func (s *summaryCountingPromptStore) Resolve(ctx context.Context, id prompt.Ident, scope prompt.Scope) (*prompt.Override, error) {
	s.resolves++
	return s.Store.Resolve(ctx, id, scope)
}
func replaceSummaryEntries(t *testing.T, rt *Runtime, in *SummarizeContextInput, entries []index.MessageEntry) {
	t.Helper()
	seg, err := index.NewSegment("", "", entries)
	require.NoError(t, err)
	data, _, err := index.EncodeSegment(seg)
	require.NoError(t, err)
	in.ViewRef.IndexHead, err = rt.RunStore.Put(t.Context(), "root", data)
	require.NoError(t, err)
}
func recordSummaryAcceptanceRequest(t *testing.T, rt *Runtime, client *summaryTestClient, messages []*model.Message) error {
	t.Helper()
	rec := NewCallRecording(rt.RunStore, "root", index.ViewRef{}, nil, 10)
	cli := &callInfoClientWrapper{store: rt.RunStore, info: model.CallInfo{AgentID: "agent", RunID: "run", RootRunID: "root", ModelID: "summary", Attempt: 1}, inner: &callRecordingClient{inner: client, recording: rec}}
	_, err := cli.Complete(t.Context(), &model.Request{Messages: messages})
	return err
}

func TestSummaryNonTextReceipts(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		rt, in, client, entries := summaryFixture(t)
		data, err := json.Marshal(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "canonical user fact"}, model.ImagePart{Format: "png", Bytes: []byte("image bytes")}}})
		require.NoError(t, err)
		entries[0].Hash, err = rt.RunStore.Put(t.Context(), "root", data)
		require.NoError(t, err)
		replaceSummaryEntries(t, rt, in, entries)
		in.ContextModel.AcceptsImages = accepted
		out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
		require.NoError(t, err)
		require.Empty(t, out.Kind)
		imageFound := false
		for _, msg := range client.requests[0].Messages {
			for _, part := range msg.Parts {
				if _, ok := part.(model.ImagePart); ok {
					imageFound = true
				}
			}
		}
		assert.Equal(t, accepted, imageFound)
		source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
			return rt.RunStore.Get(ctx, "root", h)
		}
		committed, err := index.Walk(t.Context(), source, in.ViewRef.IndexHead)
		require.NoError(t, err)
		candidate, err := checkpoint.FromRecord(t.Context(), source, *out.Checkpoint, committed, 0)
		require.NoError(t, err)
		msg, err := checkpoint.Render(candidate, in.Budget, checkpoint.DefaultLimits, nil)
		require.NoError(t, err)
		if accepted {
			assert.NotContains(t, msg.Parts[0].(model.TextPart).Text, "not summarised: image")
		} else {
			assert.Contains(t, msg.Parts[0].(model.TextPart).Text, "not summarised: image")
		}
	}
}

func TestSummaryLimitsUnknownNoCall(t *testing.T) {
	rt, in, client, _ := summaryFixture(t)
	client.limits.ContextWindow = 0
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	assert.Equal(t, "summariser_limits_unknown", out.Kind)
	assert.Nil(t, out.Checkpoint)
	assert.Empty(t, out.CallKeys)
	assert.Empty(t, client.requests)
}

func TestSummaryRegisteredIDDoesNotOverrideProviderModel(t *testing.T) {
	rt, in, client, _ := summaryFixture(t)
	in.ContextModel.Class = model.ModelClassSmall
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
	require.NoError(t, err)
	require.Empty(t, out.Kind)
	require.NotEmpty(t, client.probes)
	for _, req := range client.probes {
		assert.Empty(t, req.Model, "registered ID is not a provider model name")
		assert.Equal(t, model.ModelClassSmall, req.ModelClass)
	}
	assert.Empty(t, client.requests[0].Model)
	assert.Equal(t, model.ModelClassSmall, client.requests[0].ModelClass)
	assert.Equal(t, "provider-summary", out.Facts[0].Model)
}

func TestSummaryFailurePolicyWithRecordedPartialResponse(t *testing.T) {
	transient := model.NewProviderError("test", "complete", 503, model.ProviderErrorKindUnavailable, "", "busy", "", true, nil)
	overflow := model.NewProviderError("test", "complete", 400, model.ProviderErrorKindContextOverflow, "", "full", "", false, nil)
	for _, test := range []struct {
		name, kind string
		failure    error
		calls      int
	}{
		{"transient", "summariser_error", transient, 3}, {"overflow", "summariser_overflow", overflow, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			client.errors = []error{test.failure, test.failure, test.failure}
			client.partialOnError = true
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
			require.NoError(t, err)
			assert.Equal(t, test.kind, out.Kind)
			require.Len(t, out.CallKeys, test.calls)
			for _, key := range out.CallKeys {
				call, err := summaryTestCall(t.Context(), rt.RunStore, "root", key)
				require.NoError(t, err)
				completion, err := callrecord.DecodeCompletion(call.Completion)
				require.NoError(t, err)
				assert.NotNil(t, completion.Response, "partial provider responses remain durable")
			}
		})
	}
}

func TestSummarySizeUsesRecoveryProjection(t *testing.T) {
	for _, mode := range []string{"preview", "forced placeholder"} {
		t.Run(mode, func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			if mode == "preview" {
				in.ViewRef.PreviewThrough = 3
				in.ViewRef.Budget.PreviewTokens = 128
			} else {
				in.ViewRef.ForceClearThrough = 3
			}
			client.response = summaryResponse(strings.Repeat("x", 1900))
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
			require.NoError(t, err)
			assert.Equal(t, "not_smaller", out.Kind)
			assert.Nil(t, out.Checkpoint)
		})
	}
}
