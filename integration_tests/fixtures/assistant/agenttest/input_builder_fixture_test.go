package agenttest_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	assistantapi "example.com/assistant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestInputBuilderReusesApplicationPromptFixture(t *testing.T) {
	input := figmaPromptInput("Checkout")
	turn := agenttest.Respond(agenttest.Text("ready")).When(exactPromptMessages(input))
	h := agenttest.New(t, registerScenarioFixture, agenttest.ScriptedModel("fixture", turn, turn))
	direct, err := h.Run(t.Context(), "assistant.assistant_runtime", input)
	require.NoError(t, err)
	require.NoError(t, direct.RunErr)
	s := runnerScenario()
	s.Items = eval.Items(eval.Item{ID: "checkout", Input: "Checkout", ExpectedTools: &expect.ExpectedTools{Exact: true}})
	// snippet:start eval-input-builder
	s.BuildInputs = func(_ context.Context, item eval.Item) ([]agenttest.Input, error) {
		return []agenttest.Input{figmaPromptInput(item.Input)}, nil
	}
	out, err := eval.Execute(t.Context(), h, s, s.Items.Items()[0])
	// snippet:end eval-input-builder
	require.NoError(t, err)
	require.NoError(t, out.Err)
	require.Len(t, out.Session.Runs, 1)
	require.NoError(t, out.Session.Runs[0].RunErr)
	assert.Equal(t, "ready", out.Session.Runs[0].Output.Final.Parts[0].(model.TextPart).Text)
	assert.Equal(t, 2, len(h.Script("fixture").Requests()))
}

func TestInputBuilderPreservesTurnOrderWithoutTranscriptFixture(t *testing.T) {
	first, second := figmaPromptInput("First"), figmaPromptInput("Second")
	h := agenttest.New(t, registerScenarioFixture, agenttest.ScriptedModel("fixture",
		agenttest.Respond(agenttest.Text("first answer")).When(exactPromptMessages(first)),
		agenttest.Respond(agenttest.Text("second answer")).When(exactPromptMessages(second)),
	))
	s := runnerScenario()
	s.Items = eval.Items(eval.Item{ID: "two", Turns: []string{"First", "Second"}, ExpectedTools: &expect.ExpectedTools{Exact: true}})
	s.BuildInputs = func(_ context.Context, item eval.Item) ([]agenttest.Input, error) {
		return []agenttest.Input{figmaPromptInput(item.Turns[0]), figmaPromptInput(item.Turns[1])}, nil
	}
	out, err := eval.Execute(t.Context(), h, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.NoError(t, out.Err)
	require.Len(t, out.Session.Runs, 2)
	require.NoError(t, out.Session.Runs[0].RunErr)
	require.NoError(t, out.Session.Runs[1].RunErr)
	assert.Equal(t, out.Session.Runs[0].SessionID, out.Session.Runs[1].SessionID)
	assert.Equal(t, "first answer", out.Session.Runs[0].Output.Final.Parts[0].(model.TextPart).Text)
	assert.Equal(t, "second answer", out.Session.Runs[1].Output.Final.Parts[0].(model.TextPart).Text)
}

// figmaPromptInput reuses the application's prompt assembly for normal and eval runs.
func figmaPromptInput(screen string) agenttest.Input {
	prompt := assistantapi.FixtureImplementationPrompt(screen, "React", "figma://tokens", nil)
	return agenttest.Input{
		{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "Implement the supplied design."}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{
			model.TextPart{Text: prompt},
			model.ImagePart{Format: "png", Bytes: []byte{1, 2}},
			model.DocumentPart{Name: "specification", Format: "pdf", URI: "https://example.test/specification.pdf"},
		}},
	}
}

// exactPromptMessages also rejects transcript copying between standard-driver turns.
func exactPromptMessages(input agenttest.Input) agenttest.Match {
	return func(request *model.Request) error {
		if !reflect.DeepEqual(request.Messages, []*model.Message(input)) {
			return fmt.Errorf("application prompt message roles, parts or order changed")
		}
		return nil
	}
}
