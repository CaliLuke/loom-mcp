package agenttest_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	generated "example.com/assistant/gen/assistant/agents/assistant_runtime"
	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type guidePlanner struct{}

// snippet:start wiring
func TestGuideLookupWiring(t *testing.T) {
	h := agenttest.New(t, guideRegister, agenttest.ScriptedModel("fixture",
		agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool,
			&projected.ProjectedLookupToolPayload{Query: "order 1234"})),
		agenttest.Respond(agenttest.Text("Order 1234 ships tomorrow.")),
	))
	agenttest.Stub(h, projected.ProjectedLookupToolTool).
		Return(&projected.ProjectedLookupToolResult{Answer: "ships tomorrow", Source: "orders"}).
		When(agenttest.ArgsEqual(projected.ProjectedLookupToolTool,
			&projected.ProjectedLookupToolPayload{Query: "order 1234"}))
	r, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
	require.NoError(t, err)
	require.NoError(t, r.RunErr)
	expect.Require(t, expect.Trajectory(r.Trajectory),
		expect.Call(projected.ProjectedLookupToolTool, guideQuery, func(r *projected.ProjectedLookupToolResult) error {
			if r.Answer != "ships tomorrow" || r.Source != "orders" {
				return fmt.Errorf("unexpected lookup result: %+v", r)
			}
			return nil
		}),
		expect.Completed(),
	)
}

// snippet:end wiring

func TestGuideLookupSelection(t *testing.T) {
	// Exercise live opt-in with a deterministic stand-in, never a real provider.
	t.Setenv("LOOM_LIVE_MODEL", "1")
	client, script := agenttest.ScriptClient(t, guideSelectionTurns()...)
	guideSelection(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("fixture", client); err != nil {
			return err
		}
		return guideRegister(ctx, rt)
	})
	assert.Zero(t, script.Remaining())
}

// snippet:start fixture_reuse
func TestGuideFixtureReuse(t *testing.T) {
	h := agenttest.New(t, guideRegister, agenttest.ScriptedModel("fixture"))
	r, err := h.CaptureFirstRequest(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, agenttest.SaveRequest(path, r))
	loaded := agenttest.LoadRequest(t, path)
	turn := agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool,
		&projected.ProjectedLookupToolPayload{Query: "order 1234"}))
	first, firstScript := agenttest.ScriptClient(t, turn)
	second, secondScript := agenttest.ScriptClient(t, turn)
	// Decide reuses either request directly, without a harness.
	assert.Equal(t, agenttest.Decide(t, first, r).Calls, agenttest.Decide(t, second, loaded).Calls)
	assert.Zero(t, firstScript.Remaining())
	assert.Zero(t, secondScript.Remaining())
}

// snippet:end fixture_reuse

// snippet:start diagnose
func TestGuideDiagnoseStubError(t *testing.T) {
	// NewHarness lets this test inspect the expected Close error itself.
	h, err := agenttest.NewHarness(t.Context(), guideRegister, agenttest.ScriptedModel("fixture",
		agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool,
			&projected.ProjectedLookupToolPayload{Query: "order 1234"})),
		agenttest.Respond(agenttest.Text("Lookup unavailable.")),
	))
	require.NoError(t, err)
	t.Cleanup(func() {
		var stubErr *agenttest.StubError
		assert.ErrorAs(t, h.Close(), &stubErr)
	})
	r, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
	var stubErr *agenttest.StubError
	require.ErrorAs(t, err, &stubErr)
	assert.Equal(t, projected.ProjectedLookupTool, stubErr.Tool)
	require.NotNil(t, r)
	require.NoError(t, r.RunErr) // The planner completed after seeing a tool error.
	t.Log(r.Trajectory.Render(trajectory.RenderOptions{Limits: trajectory.DefaultLimits()}))
}

// snippet:end diagnose

func TestGuideRuntimeReferences(t *testing.T) {
	h := agenttest.New(t, guideRegister, agenttest.ScriptedModel("fixture",
		agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})),
		agenttest.Respond(agenttest.Text("ready")),
	))
	// snippet:start runtime_stubs
	stub := agenttest.Stub(h, projected.ProjectedStatusToolTool)
	stub.Return(&projected.ProjectedStatusToolResult{Status: "ready"})
	// snippet:end runtime_stubs
	r, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("status?"))
	require.NoError(t, err)
	require.NoError(t, r.RunErr)
	// snippet:start runtime_trajectory
	tr, err := trajectory.Build(r.Record, r.RunID, trajectory.Options{
		Output:           r.Output,
		EventsUnverified: h.Runtime().HasEventInterceptors(),
	})
	require.NoError(t, err)
	data, err := tr.Marshal()
	require.NoError(t, err)
	// snippet:end runtime_trajectory
	assert.NotEmpty(t, data)
	// snippet:start runtime_render
	text := tr.Render(trajectory.RenderOptions{
		Format: trajectory.FormatText,
		Limits: trajectory.DefaultLimits(),
	})
	markdown := trajectory.Session{tr}.Render(trajectory.RenderOptions{
		Format: trajectory.FormatMarkdown,
		Limits: trajectory.DefaultLimits(),
	})
	// snippet:end runtime_render
	assert.Contains(t, text, "ready")
	assert.Contains(t, markdown, "ready")
	// snippet:start runtime_document
	data, err = trajectory.EncodeDocument(tr)
	require.NoError(t, err)
	// snippet:end runtime_document
	assert.Contains(t, string(data), trajectory.SchemaVersion)
	// snippet:start runtime_expect
	subject := expect.Trajectory(tr)
	expect.Require(t, subject,
		expect.Call(projected.ProjectedStatusToolTool, nil, nil),
		expect.Completed(),
		expect.MaxModelCalls(3),
		expect.MaxTokens(1000),
	)
	// snippet:end runtime_expect
}

func TestGuideRuntimeHarness(t *testing.T) {
	// snippet:start runtime_harness
	h := agenttest.New(t, guideRegister,
		agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("done"))),
		agenttest.WithResponder(agenttest.Responder{agenttest.Clarify("yes")}),
	)
	result, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("hello"))
	require.NoError(t, err)
	expect.Require(t, expect.Trajectory(result.Trajectory), expect.Completed())
	// snippet:end runtime_harness
}

// snippet:start selection
func guideSelection(t testing.TB, register agenttest.Register) {
	t.Helper()
	h := agenttest.New(t, register, agenttest.LiveModel("fixture"))
	r, err := h.CaptureFirstRequest(t.Context(), "assistant.assistant_runtime", agenttest.User("Where is order 1234?"))
	require.NoError(t, err)
	agenttest.Repeat(t, 5, 4, func(at testing.TB) {
		d := agenttest.Decide(at, h.Model("fixture"), r)
		expect.Require(at, expect.Calls(d.Calls), expect.Call(projected.ProjectedLookupToolTool, guideQuery, nil))
	})
	negative := r.WithUserMessage("What are your opening hours?")
	agenttest.Repeat(t, 5, 5, func(at testing.TB) {
		d := agenttest.Decide(at, h.Model("fixture"), negative)
		expect.Require(at, expect.Calls(d.Calls), expect.Forbid(projected.ProjectedLookupToolTool))
	})
}

// snippet:end selection

// snippet:start guide_registration
func guideRegister(ctx context.Context, rt *runtime.Runtime) error {
	if err := generated.RegisterUsedToolsets(ctx, rt); err != nil {
		return err
	}
	return generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: guidePlanner{}})
}

func guideQuery(p *projected.ProjectedLookupToolPayload) error {
	if p.Query != "order 1234" {
		return fmt.Errorf("query = %q", p.Query)
	}
	return nil
}

// snippet:end guide_registration

// guideSelectionTurns provides five matching decisions and five negative decisions.
func guideSelectionTurns() []agenttest.Turn {
	turns := make([]agenttest.Turn, 0, 10)
	for range 5 {
		turns = append(turns, agenttest.Respond(agenttest.ToolCall(projected.ProjectedLookupToolTool,
			&projected.ProjectedLookupToolPayload{Query: "order 1234"})).When(agenttest.LastUserContains("1234")))
	}
	for range 5 {
		turns = append(turns, agenttest.Respond(agenttest.Text("We open at nine.")).When(agenttest.LastUserContains("opening hours")))
	}
	return turns
}

// guideTurn advertises the generated lookup and status schemas, propagating model errors.
func guideTurn(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("fixture")
	if !ok {
		return nil, fmt.Errorf("guide model missing")
	}
	definitions := make([]*model.ToolDefinition, 0, 2)
	for _, spec := range projected.Specs {
		if spec.Name != projected.ProjectedLookupTool && spec.Name != projected.ProjectedStatusTool {
			continue
		}
		schema, err := model.DecodeSchemaJSON(spec.Payload.Schema)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, &model.ToolDefinition{
			Name: spec.Name.String(), Description: spec.Description, InputSchema: schema,
		})
	}
	response, err := client.Complete(ctx, &model.Request{
		Model: "scripted", Messages: messages, Tools: definitions,
	})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{}
	for _, call := range response.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: call.Name, ToolCallID: call.ID, Payload: call.Payload})
	}
	if len(result.ToolCalls) == 0 {
		result.FinalResponse = &planner.FinalResponse{Message: &response.Content[0]}
	}
	return result, nil
}

func (guidePlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	return guideTurn(ctx, input.Agent, input.Messages)
}

func (guidePlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return guideTurn(ctx, input.Agent, input.Messages)
}
