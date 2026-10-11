package agenttest

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type recordingTB struct {
	testing.TB
	cleanups []func()
	errors   []string
}

func TestScriptFIFO(t *testing.T) {
	client, script := ScriptClient(t, Respond(RawToolCall("helpers.search", []byte(`{"query":"go"}`))), Respond(Text("done")))
	request := scriptRequest()
	first, err := client.Complete(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, "scripted-1-1", first.ToolCalls[0].ID)
	assert.Equal(t, tools.Ident("helpers.search"), first.ToolCalls[0].Name)
	assert.Equal(t, "tool_use", first.StopReason)
	assert.Equal(t, model.TokenUsage{Model: "test-model", ModelClass: "small"}, first.Usage)
	second, err := client.Complete(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}, second.Content)
	assert.Equal(t, "end_turn", second.StopReason)
	assert.Equal(t, client, script.Client())
	assert.Zero(t, script.Remaining())
	assert.Len(t, script.Requests(), 2)
	assert.Equal(t, request, script.Requests()[0])
	requests := script.Requests()
	requests[0] = nil
	assert.NotNil(t, script.Requests()[0])
}

func TestScriptOutputValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		turn Turn
		kind model.OutputValidationKind
	}{
		{"unknown tool", Respond(RawToolCall("missing.tool", []byte(`{}`))), model.OutputValidationToolIdentity},
		{"bad arguments", Respond(RawToolCall("helpers.search", []byte(`{"query":42}`))), model.OutputValidationToolArguments},
		{"output limit", Respond(Text("partial"), OutputLimited()), model.OutputValidationOutputBounds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, script := ScriptClient(t, tc.turn)
			response, err := client.Complete(t.Context(), scriptRequest())
			assert.Nil(t, response)
			var rejection *model.OutputValidationError
			require.ErrorAs(t, err, &rejection)
			assert.Equal(t, tc.kind, rejection.Kind())
			assert.Zero(t, script.Remaining())
		})
	}
}

func TestScriptProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		kind   model.ProviderErrorKind
		status int
		retry  bool
	}{
		{model.ProviderErrorKindAuth, 401, false},
		{model.ProviderErrorKindInvalidRequest, 400, false},
		{model.ProviderErrorKindContextOverflow, 400, false},
		{model.ProviderErrorKindRateLimited, 429, true},
		{model.ProviderErrorKindUnavailable, 503, true},
		{model.ProviderErrorKindUnknown, 500, false},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			client, script := ScriptClient(t, ProviderFailure(tc.kind, "failure"))
			response, err := client.Complete(t.Context(), scriptRequest())
			assert.Nil(t, response)
			provider, ok := model.AsProviderError(err)
			require.True(t, ok)
			assert.Equal(t, tc.kind, provider.Kind())
			assert.Equal(t, tc.retry, provider.Retryable())
			assert.Equal(t, tc.status, provider.HTTPStatus())
			assert.Equal(t, "scripted", provider.Provider())
			assert.Equal(t, "request", provider.Operation())
			assert.Equal(t, "failure", provider.Message())
			require.NoError(t, script.Err())
		})
	}
	client, script := ScriptClient(t, ContextOverflow())
	response, err := client.Complete(t.Context(), scriptRequest())
	assert.Nil(t, response)
	assert.Equal(t, contextmeasure.OutcomeOverflow, (contextmeasure.RequestFacts{}).WithResult(model.TokenUsage{}, err).Outcome)
	require.NoError(t, script.Err())
}

func TestScriptDiagnostics(t *testing.T) {
	script, err := NewScript(Respond(Text("one")), Respond(Text("two")))
	require.NoError(t, err)
	for range 2 {
		response, callErr := script.Client().Complete(t.Context(), scriptRequest())
		require.NoError(t, callErr)
		assert.NotNil(t, response)
	}
	response, err := script.Client().Complete(t.Context(), scriptRequest())
	assert.Nil(t, response)
	require.ErrorIs(t, err, ErrScriptExhausted)
	require.ErrorContains(t, err, "call 3")
	require.ErrorContains(t, err, "test-model")
	require.ErrorIs(t, script.Err(), ErrScriptExhausted)
	assert.Len(t, script.Requests(), 3)
	assertNotProviderError(t, err)
}

func TestScriptMatcherFailureDoesNotConsume(t *testing.T) {
	mismatch := errors.New("expected another user")
	script, err := NewScript(Respond(Text("ok")).When(func(*model.Request) error { return mismatch }))
	require.NoError(t, err)
	response, err := script.Client().Complete(t.Context(), scriptRequest())
	assert.Nil(t, response)
	var diagnostic *ScriptError
	require.ErrorAs(t, err, &diagnostic)
	require.ErrorIs(t, err, mismatch)
	for _, text := range []string{"call 1", "turn 1", "expected another user", "test-model", "user", "find go", "helpers.search"} {
		require.ErrorContains(t, err, text)
	}
	assert.Equal(t, 1, script.Remaining())
	require.ErrorIs(t, script.Err(), mismatch)
	assertNotProviderError(t, err)
}

func TestScriptMatchers(t *testing.T) {
	request := scriptRequest()
	request.Messages = append(request.Messages,
		&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "helpers.search", Input: map[string]any{"query": "go"}}}},
		&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: map[string]any{"answer": "found go"}}}},
	)
	for _, tc := range []struct {
		name  string
		match Match
		pass  bool
	}{
		{"last user excludes older message", LastUserContains("find go"), false},
		{"has tool", HasTool(tools.Ident("helpers.search")), true},
		{"missing tool", HasTool(tools.Ident("other.tool")), false},
		{"lacks tool", LacksTool(tools.Ident("other.tool")), true},
		{"does not lack", LacksTool(tools.Ident("helpers.search")), false},
		{"last tool result", LastToolResult(tools.Ident("helpers.search"), "found go"), true},
		{"wrong result", LastToolResult(tools.Ident("helpers.search"), "missing"), false},
		{"wrong tool", LastToolResult(tools.Ident("other.tool"), "found go"), false},
		{"model class", ModelClass("small"), true},
		{"wrong class", ModelClass("large"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.pass {
				require.NoError(t, tc.match(request))
			} else {
				require.Error(t, tc.match(request))
			}
		})
	}
	require.NoError(t, LastUserContains("find")(scriptRequest()))
}

func TestScriptCleanupReportsEveryUnusedTurn(t *testing.T) {
	recorder := &recordingTB{TB: t}
	client, script := ScriptClient(recorder, Respond(Text("one")), Respond(Text("two")))
	assert.NotNil(t, client)
	assert.Equal(t, 2, script.Remaining())
	require.Len(t, recorder.cleanups, 1)
	recorder.cleanups[0]()
	require.Len(t, recorder.errors, 1)
	assert.Contains(t, recorder.errors[0], "turn 1")
	assert.Contains(t, recorder.errors[0], "turn 2")
}

func TestScriptDuplicateExplicitIDs(t *testing.T) {
	script, err := NewScript(Respond(RawToolCall("helpers.search", []byte(`{}`)), CallID("same")), Respond(RawToolCall("helpers.search", []byte(`{}`)), CallID("same")))
	assert.Nil(t, script)
	require.ErrorContains(t, err, "same")
}

func TestScriptDeterminism(t *testing.T) {
	turn := Respond(Thinking("reason"), Text("hello"), RawToolCall("helpers.search", []byte(`{"query":"go"}`)), Usage(3, 2), StopReason("tool_use")).Chunked(2)
	first, _ := ScriptClient(t, turn, turn)
	second, _ := ScriptClient(t, turn, turn)
	a, err := first.Complete(t.Context(), scriptRequest())
	require.NoError(t, err)
	b, err := second.Complete(t.Context(), scriptRequest())
	require.NoError(t, err)
	aJSON, err := json.Marshal(a)
	require.NoError(t, err)
	bJSON, err := json.Marshal(b)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(aJSON, bJSON), "responses must be byte-identical")
	assert.Equal(t, model.TokenUsage{Model: "test-model", ModelClass: "small", InputTokens: 3, OutputTokens: 2, TotalTokens: 5}, a.Usage)
	firstStream, err := first.Stream(t.Context(), scriptRequest())
	require.NoError(t, err)
	secondStream, err := second.Stream(t.Context(), scriptRequest())
	require.NoError(t, err)
	firstChunks, terminal := drainScriptStream(firstStream)
	require.ErrorIs(t, terminal, io.EOF)
	require.NoError(t, firstStream.Finalize(nil))
	secondChunks, terminal := drainScriptStream(secondStream)
	require.ErrorIs(t, terminal, io.EOF)
	require.NoError(t, secondStream.Finalize(nil))
	firstChunkJSON, err := json.Marshal(firstChunks)
	require.NoError(t, err)
	secondChunkJSON, err := json.Marshal(secondChunks)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(firstChunkJSON, secondChunkJSON), "stream chunks must be byte-identical")
	aStreamJSON, err := json.Marshal(firstStream.Response())
	require.NoError(t, err)
	bStreamJSON, err := json.Marshal(secondStream.Response())
	require.NoError(t, err)
	assert.True(t, bytes.Equal(aStreamJSON, bStreamJSON), "stream responses must be byte-identical")
}

func TestScriptConcurrentCalls(t *testing.T) {
	turns := make([]Turn, 20)
	for i := range turns {
		turns[i] = Respond(Text("ok"))
	}
	client, script := ScriptClient(t, turns...)
	results := make(chan error, len(turns))
	var workers sync.WaitGroup
	for range turns {
		workers.Go(func() {
			response, err := client.Complete(t.Context(), scriptRequest())
			results <- err
			assert.NotNil(t, response)
			assert.NotNil(t, script.Requests())
			assert.GreaterOrEqual(t, script.Remaining(), 0)
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	assert.Len(t, script.Requests(), len(turns))
	assert.Zero(t, script.Remaining())
}

func TestScriptAnyOrder(t *testing.T) {
	script, err := NewScript(Respond(Text("first")).When(LastUserContains("first")), Respond(Text("second")).When(LastUserContains("second")), Respond(Text("fallback")))
	require.NoError(t, err)
	script.AnyOrder()
	for _, text := range []string{"second", "first", "other"} {
		request := scriptRequest()
		request.Messages[0].Parts = []model.Part{model.TextPart{Text: text}}
		response, callErr := script.Client().Complete(t.Context(), request)
		require.NoError(t, callErr)
		assert.NotEmpty(t, response.Content)
		if text == "other" {
			text = "fallback"
		}
		assert.Equal(t, model.TextPart{Text: text}, response.Content[0].Parts[0])
	}
	require.NoError(t, script.Err())
}

func TestScriptContextLimits(t *testing.T) {
	script, err := NewScript()
	require.NoError(t, err)
	limits := model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 100, DefaultMaxTokens: 50, ImageTokens: 20}
	assert.Same(t, script, script.WithContextLimits(limits))
	info, ok := script.Client().(model.ContextInfo)
	require.True(t, ok)
	got, name, known := info.ContextLimits(t.Context(), scriptRequest())
	assert.Equal(t, limits, got)
	assert.Equal(t, "test-model", name)
	assert.True(t, known)
	_, counts := script.Client().(model.TokenCounter)
	assert.False(t, counts)
	assert.Same(t, script, script.WithContextLimits(model.ContextLimits{}))
	got, _, known = info.ContextLimits(t.Context(), scriptRequest())
	assert.Zero(t, got)
	assert.False(t, known)
}

func scriptRequest() *model.Request {
	return &model.Request{Model: "test-model", ModelClass: "small", Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "find go"}}}}, Tools: []*model.ToolDefinition{{Name: "helpers.search", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}}}}
}

func assertNotProviderError(t *testing.T, err error) {
	t.Helper()
	_, provider := model.AsProviderError(err)
	assert.False(t, provider)
}

func (r *recordingTB) Cleanup(fn func()) {
	r.cleanups = append(r.cleanups, fn)
}

func (r *recordingTB) Error(args ...any) {
	r.errors = append(r.errors, fmt.Sprint(args...))
}
