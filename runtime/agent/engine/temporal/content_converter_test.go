package temporal

import (
	"encoding/json/v2"
	"fmt"
	"reflect"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentReferencesSurviveTemporalAndJSON(t *testing.T) {
	converter := NewAgentDataConverter(nil)
	key := runstore.CallKey{RunID: "run", Seq: 7, Attempt: 2}
	message := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}
	for _, value := range []any{
		&api.RecordContentInput{RootRunID: "root", Items: [][]byte{[]byte("first"), []byte("second")}},
		&api.RecordContentOutput{Refs: []runstore.Hash{"sha256:first", "sha256:second"}},
		&api.PlanActivityOutput{IndexHead: "sha256:head", TranscriptRef: "sha256:transcript", Result: &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "test.tool", Payload: rawjson.Message(`{}`)}}}, ToolCallRefs: []runstore.Hash{"sha256:call"}},
		&api.PlanActivityOutput{Recovery: &api.ModelRecovery{Correction: "retry", CorrectionRef: "sha256:correction"}, CallKeys: []runstore.CallKey{key}, ProducingCall: &key, RequestGrowth: &api.RequestGrowth{Key: key, Inside: 2, Outside: 3}},
		&api.RunOutput{Final: message, FinalRef: "sha256:final"},
		&api.FinishRunInput{RootRunID: "root", Final: message},
		&api.FinishRunOutput{FinalRef: "sha256:final"},
		&api.ResumeRequest{Messages: []*model.Message{message}, MessageRefs: []runstore.Hash{"sha256:message"}},
		&api.ClarificationAnswer{Answer: "yes", AnswerRef: "sha256:answer"},
		&api.TypedInputAnswer{Payload: rawjson.Message(`{}`), PayloadRef: "sha256:payload"},
		&api.ToolResultsSet{Results: []*api.ProvidedToolResult{{Result: rawjson.Message(`{}`), Refs: tools.ResultRefs{Result: "sha256:result"}}}},
		&api.PlanActivityInput{Step: 4, View: index.ViewRef{IndexHead: "sha256:head", Pending: []index.MessageEntry{{ID: 2, Kind: index.TypedInput, Hash: "sha256:typed", InputID: "input"}}}},
	} {
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			payload, err := converter.ToPayload(value)
			require.NoError(t, err)
			// Reflection only selects the typed decode destination for the wire matrix.
			decoded := reflect.New(reflect.TypeOf(value)).Interface()
			require.NoError(t, converter.FromPayload(payload, decoded))
			want, err := json.Marshal(value)
			require.NoError(t, err)
			got, err := json.Marshal(decoded)
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got))
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, decoded))
			got, err = json.Marshal(decoded)
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got))
		})
	}
}
