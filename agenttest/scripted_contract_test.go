package agenttest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type fatalTB struct {
	*recordingTB
}

func TestScriptBuilderErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		turn Turn
	}{
		{"orphan ID", Respond(CallID("orphan"))},
		{"empty ID", Respond(RawToolCall("helpers.search", []byte(`{}`)), CallID(""))},
		{"negative usage", Respond(Usage(-1, 0))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, err := NewScript(tc.turn)
			assert.Nil(t, script)
			require.ErrorContains(t, err, "turn 1")
		})
	}
	recorder := &fatalTB{recordingTB: &recordingTB{TB: t}}
	assert.Panics(t, func() {
		ScriptClient(recorder, Respond(CallID("orphan")))
	})
	require.Len(t, recorder.errors, 1)
	assert.Contains(t, recorder.errors[0], "build model script")
	assert.Empty(t, recorder.cleanups)
}

func TestScriptRetainsDiagnosticsAfterRecovery(t *testing.T) {
	script, err := NewScript(Respond(Text("ok")).When(LastUserContains("expected"), HasTool(tools.Ident("helpers.search")), ModelClass("small")))
	require.NoError(t, err)
	response, mismatch := script.Client().Complete(t.Context(), scriptRequest())
	assert.Nil(t, response)
	require.Error(t, mismatch)
	request := scriptRequest()
	request.Messages[0].Parts = []model.Part{model.TextPart{Text: "expected"}}
	response, err = script.Client().Complete(t.Context(), request)
	require.NoError(t, err)
	assert.NotNil(t, response)
	assert.Zero(t, script.Remaining())
	require.ErrorIs(t, script.Err(), mismatch)
}

func TestScriptAnyOrderUsesFirstMatchAndDiagnosesNoMatch(t *testing.T) {
	script, err := NewScript(Respond(Text("first")).When(LastUserContains("go")), Respond(Text("second")).When(LastUserContains("go")))
	require.NoError(t, err)
	assert.Same(t, script, script.AnyOrder())
	response, err := script.Client().Complete(t.Context(), scriptRequest())
	require.NoError(t, err)
	assert.Equal(t, model.TextPart{Text: "first"}, response.Content[0].Parts[0])
	request := scriptRequest()
	request.Messages[0].Parts = []model.Part{model.TextPart{Text: "mismatch"}}
	response, err = script.Client().Complete(t.Context(), request)
	assert.Nil(t, response)
	var diagnostic *ScriptError
	require.ErrorAs(t, err, &diagnostic)
	assert.Equal(t, 2, diagnostic.CallIndex)
	assert.Equal(t, 2, diagnostic.TurnIndex)
	assert.Equal(t, 1, script.Remaining())
	require.ErrorIs(t, script.Err(), err)
	require.NotErrorIs(t, err, ErrScriptExhausted)
}

func TestScriptRequestSummaryIsBoundedAndIncludesClass(t *testing.T) {
	script, err := NewScript()
	require.NoError(t, err)
	request := scriptRequest()
	request.Model = ""
	request.Messages[0].Parts = []model.Part{model.TextPart{Text: strings.Repeat("x", 200) + "hidden suffix"}}
	response, err := script.Client().Complete(t.Context(), request)
	assert.Nil(t, response)
	require.ErrorContains(t, err, "small")
	require.ErrorContains(t, err, "…")
	assert.NotContains(t, err.Error(), "hidden suffix")
}

func TestScriptRequestsAsReceivedAndPayloadOwnership(t *testing.T) {
	payload := []byte(`{"query":"go"}`)
	turn := Respond(RawToolCall("helpers.search", payload), CallID("explicit"))
	payload[0] = '!'
	script, err := NewScript(turn)
	require.NoError(t, err)
	request := scriptRequest()
	response, err := script.Complete(t.Context(), request)
	require.NoError(t, err)
	assert.Same(t, request, script.Requests()[0])
	assert.Equal(t, "explicit", response.ToolCalls[0].ID)
	assert.JSONEq(t, `{"query":"go"}`, string(response.ToolCalls[0].Payload))
}

func TestScriptLastToolResultUsesLatestResult(t *testing.T) {
	request := scriptRequest()
	request.Messages = []*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "one", Name: "helpers.search"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "one", Content: "old result"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "two", Name: "helpers.search"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "two", Content: "new result"}}},
	}
	require.NoError(t, LastToolResult(tools.Ident("helpers.search"), "new")(request))
	require.Error(t, LastToolResult(tools.Ident("helpers.search"), "old")(request))
}

func TestScriptStreamOutputValidation(t *testing.T) {
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
			stream, err := client.Stream(t.Context(), scriptRequest())
			require.NoError(t, err)
			chunks, terminal := drainScriptStream(stream)
			for _, chunk := range chunks {
				assert.NotEqual(t, model.ChunkTypeToolCall, chunk.Kind())
			}
			var rejection *model.OutputValidationError
			require.ErrorAs(t, terminal, &rejection)
			assert.Equal(t, tc.kind, rejection.Kind())
			assert.Nil(t, stream.Response())
			require.ErrorIs(t, stream.Finalize(terminal), terminal)
			require.NoError(t, script.Err())
		})
	}
}

func TestScriptCleanupReportsRecordedFailure(t *testing.T) {
	recorder := &recordingTB{TB: t}
	client, script := ScriptClient(recorder, Respond(Text("ok")).When(HasTool(tools.Ident("missing.tool"))))
	response, err := client.Complete(t.Context(), scriptRequest())
	assert.Nil(t, response)
	require.Error(t, err)
	assert.Equal(t, 1, script.Remaining())
	recorder.cleanups[0]()
	require.Len(t, recorder.errors, 1)
	assert.Contains(t, recorder.errors[0], "call 1")
	assert.Contains(t, recorder.errors[0], "missing.tool")
	assert.Contains(t, recorder.errors[0], "unconsumed turn 1")
	require.NotErrorIs(t, err, ErrScriptExhausted)
}

func TestScriptDiagnosticHasSingleTurnPrefix(t *testing.T) {
	for _, anyOrder := range []bool{false, true} {
		name := "FIFO"
		if anyOrder {
			name = "any order"
		}
		t.Run(name, func(t *testing.T) {
			script, err := NewScript(Respond(Text("ok")).When(nil))
			require.NoError(t, err)
			if anyOrder {
				script.AnyOrder()
			}
			response, err := script.Client().Complete(t.Context(), scriptRequest())
			assert.Nil(t, response)
			require.ErrorContains(t, err, "call 1, turn 1: nil request matcher")
			assert.Equal(t, 1, strings.Count(err.Error(), "turn 1:"))
			assert.Equal(t, 1, script.Remaining())
		})
	}
}

func (r *fatalTB) Fatalf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
	panic("recorded fatal")
}
