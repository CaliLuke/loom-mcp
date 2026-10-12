package runtime_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfirmedToolPauseIsAnsweredBeforeResume(t *testing.T) {
	script, err := agenttest.NewScript(
		agenttest.Respond(agenttest.RawToolCall("resume.first", rawjson.Message(`{}`)), agenttest.CallID("first")),
		agenttest.Respond(agenttest.Text("done")),
	)
	require.NoError(t, err)
	var executed []tools.Ident
	h := agenttest.New(t, registerAwaitResume(awaitResumePath{confirmation: true, pause: true}, script.Client(), &executed),
		agenttest.WithResponder(agenttest.Responder{agenttest.Approve(tools.Ident("resume.first")), agenttest.Clarify("continue now")}), agenttest.PassthroughAll())
	result, err := h.Run(t.Context(), "resume", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	require.Len(t, result.Awaits, 2)
	assert.Equal(t, hooks.AwaitConfirmation, result.Awaits[0].Kind)
	assert.Equal(t, hooks.AwaitClarification, result.Awaits[1].Kind)
	assert.NotNil(t, result.Awaits[1].Answer)
	requests := script.Requests()
	require.Len(t, requests, 2)
	last := requests[1].Messages[len(requests[1].Messages)-1]
	assert.Equal(t, model.ConversationRoleUser, last.Role)
	assert.Equal(t, []model.Part{model.TextPart{Text: "continue now"}}, last.Parts)
	assert.Equal(t, []tools.Ident{"resume.first"}, executed)
	assert.Equal(t, 2, result.Trajectory.Totals.AdoptedModelCalls)
}
