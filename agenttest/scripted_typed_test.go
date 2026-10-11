package agenttest

import (
	"encoding/json/v2"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type typedSearchPayload struct {
	Term string
}

func TestScriptTypedToolCall(t *testing.T) {
	tool := typedSearchTool()
	for _, streaming := range []bool{false, true} {
		name := "complete"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			client, script := ScriptClient(t, Respond(ToolCall(tool, typedSearchPayload{Term: "go"}), CallID("typed-call")).When(HasTool(tool)))
			var response *model.Response
			if streaming {
				stream, err := client.Stream(t.Context(), scriptRequest())
				require.NoError(t, err)
				chunks, terminal := drainScriptStream(stream)
				require.ErrorIs(t, terminal, io.EOF)
				require.NoError(t, stream.Finalize(nil))
				assert.Len(t, chunks, 3)
				response = stream.Response()
			} else {
				var err error
				response, err = client.Complete(t.Context(), scriptRequest())
				require.NoError(t, err)
			}
			require.NotNil(t, response)
			require.Len(t, response.ToolCalls, 1)
			call := response.ToolCalls[0]
			assert.Equal(t, tool.Ident(), call.Name)
			assert.Equal(t, "typed-call", call.ID)
			assert.JSONEq(t, `{"query":"go"}`, string(call.Payload))
			decoded, err := tool.Payload.FromJSON(call.Payload)
			require.NoError(t, err)
			assert.Equal(t, typedSearchPayload{Term: "go"}, decoded)
			assert.Equal(t, "tool_use", response.StopReason)
			assert.Zero(t, script.Remaining())
		})
	}
}

func TestScriptTypedToolCallCodecFailure(t *testing.T) {
	tool := tools.TypedTool[any, any]{Name: "helpers.search", Payload: tools.AnyJSONCodec}
	script, err := NewScript(Respond(ToolCall(tool, any(make(chan int)))))
	assert.Nil(t, script)
	require.ErrorContains(t, err, "turn 1")
	require.ErrorContains(t, err, "helpers.search")
	require.ErrorContains(t, err, "chan int")
}

func TestScriptTypedToolCallPreservesCodecError(t *testing.T) {
	failure := errors.New("payload encode failed")
	tool := typedSearchTool()
	tool.Payload.ToJSON = func(typedSearchPayload) ([]byte, error) {
		return nil, failure
	}
	script, err := NewScript(Respond(ToolCall(tool, typedSearchPayload{Term: "go"})))
	assert.Nil(t, script)
	require.ErrorIs(t, err, failure)
}

func TestScriptTypedToolCallMissingEncoder(t *testing.T) {
	tool := tools.TypedTool[typedSearchPayload, string]{Name: "helpers.search"}
	script, err := NewScript(Respond(ToolCall(tool, typedSearchPayload{Term: "go"})))
	assert.Nil(t, script)
	require.ErrorContains(t, err, "turn 1")
	require.ErrorContains(t, err, "helpers.search")
	require.ErrorContains(t, err, "encoder")
}

func TestScriptNamedMatchers(t *testing.T) {
	request := scriptRequest()
	request.Messages = append(request.Messages,
		&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "helpers.search"}}},
		&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "found go"}}},
	)
	other := typedSearchTool()
	other.Name = "other.tool"
	for _, tc := range []struct {
		name    string
		tool    tools.Named
		present bool
	}{
		{"descriptor present", typedSearchTool(), true},
		{"descriptor absent", other, false},
		{"ident present", tools.Ident("helpers.search"), true},
		{"ident absent", tools.Ident("other.tool"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.present {
				require.NoError(t, HasTool(tc.tool)(request))
				require.Error(t, LacksTool(tc.tool)(request))
				require.NoError(t, LastToolResult(tc.tool, "found go")(request))
				require.Error(t, LastToolResult(tc.tool, "missing")(request))
			} else {
				require.Error(t, HasTool(tc.tool)(request))
				require.NoError(t, LacksTool(tc.tool)(request))
				require.Error(t, LastToolResult(tc.tool, "found go")(request))
			}
		})
	}
}

func typedSearchTool() tools.TypedTool[typedSearchPayload, string] {
	return tools.TypedTool[typedSearchPayload, string]{
		Name: "helpers.search",
		Payload: tools.JSONCodec[typedSearchPayload]{
			ToJSON: func(payload typedSearchPayload) ([]byte, error) {
				return json.Marshal(map[string]string{"query": payload.Term})
			},
			FromJSON: func(data []byte) (typedSearchPayload, error) {
				var wire struct {
					Query string `json:"query"`
				}
				if err := json.Unmarshal(data, &wire); err != nil {
					return typedSearchPayload{}, err
				}
				return typedSearchPayload{Term: wire.Query}, nil
			},
		},
	}
}
