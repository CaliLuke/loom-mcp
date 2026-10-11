package runtime_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type awaitResumePlanner struct {
	await *planner.Await
	specs []tools.ToolSpec
}

type awaitResumePath struct {
	name         string
	await        *planner.Await
	responder    agenttest.AwaitRule
	confirmation bool
	pause        bool
	denied       bool
}

// TestWorkflowAwaitResumeAdoptsOnce exercises the real workflow and recorded model
// calls. Call three is deliberately distinct so a discarded call two is visible.
func TestWorkflowAwaitResumeAdoptsOnce(t *testing.T) {
	for _, path := range awaitResumePaths() {
		for _, chooseTool := range []bool{false, true} {
			name := "final"
			if chooseTool {
				name = "tool"
			}
			t.Run(path.name+"/"+name, func(t *testing.T) {
				second := agenttest.Respond(agenttest.Text("call two final"))
				wantCalls, wantFinal := 2, "call two final"
				wantExecuted := []tools.Ident{"resume.first"}
				if path.denied {
					wantExecuted = nil
				}
				if chooseTool {
					second = agenttest.Respond(agenttest.Text("call two tool"), agenttest.RawToolCall("resume.second", rawjson.Message(`{}`)), agenttest.CallID("second"))
					wantCalls, wantFinal = 3, "call three final"
					wantExecuted = append(wantExecuted, "resume.second")
				}
				script, err := agenttest.NewScript(
					agenttest.Respond(agenttest.Text("call one tool"), agenttest.RawToolCall("resume.first", rawjson.Message(`{}`)), agenttest.CallID("first")),
					second, agenttest.Respond(agenttest.Text("call three final")),
				)
				require.NoError(t, err)
				var executed []tools.Ident
				h := agenttest.New(t, registerAwaitResume(path, script.Client(), &executed), agenttest.WithResponder(agenttest.Responder{path.responder}), agenttest.PassthroughAll())

				result, err := h.Run(t.Context(), "resume", nil)

				require.NoError(t, err)
				require.NoError(t, result.RunErr)
				require.NotNil(t, result.Output.Final)
				assert.Equal(t, wantFinal, result.Output.Final.Parts[0].(model.TextPart).Text)
				assert.Len(t, script.Requests(), wantCalls, "one logical resume after the await")
				assert.Equal(t, wantExecuted, executed, "the tool selected by call two must execute")
				assert.Equal(t, wantCalls, result.Trajectory.Totals.AdoptedModelCalls)
				assertAdoptedToolsActedOn(t, result.Trajectory)
			})
		}
	}
}

func (p awaitResumePlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	result, err := p.complete(ctx, in.Agent, in.Messages)
	if err != nil {
		return nil, err
	}
	result.Await = p.await
	return result, nil
}

func (p awaitResumePlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages)
}

func awaitResumePaths() []awaitResumePath {
	return []awaitResumePath{
		{name: "approve", confirmation: true, responder: agenttest.Approve(tools.Ident("resume.first"))},
		{name: "deny", confirmation: true, denied: true, responder: agenttest.Deny(tools.Ident("resume.first"))},
		{name: "clarification", await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "await", Question: "which?"})), responder: agenttest.Clarify("yes")},
		{name: "questions", await: planner.NewAwait(planner.AwaitQuestionsItem(&planner.AwaitQuestions{ID: "await", ToolName: "resume.external", ToolCallID: "external", Payload: rawjson.Message(`{}`), Questions: []planner.AwaitQuestion{{ID: "q", Prompt: "pick", Options: []planner.AwaitQuestionOption{{ID: "yes", Label: "Yes"}}}}})), responder: agenttest.Answer("q", "yes")},
		{name: "typed", await: planner.NewAwait(planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: "await", Title: "input", Schema: rawjson.Message(`{"type":"object"}`)})), responder: agenttest.TypedInput(rawjson.Message(`{"answer":"yes"}`))},
		{name: "external", await: planner.NewAwait(planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "await", Items: []planner.AwaitToolItem{{Name: "resume.external", ToolCallID: "external", Payload: rawjson.Message(`{}`)}}})), responder: agenttest.ExternalToolsFunc(func(event *hooks.AwaitExternalToolsEvent) ([]*api.ProvidedToolResult, error) {
			return []*api.ProvidedToolResult{{Name: event.Items[0].ToolName, ToolCallID: event.Items[0].ToolCallID, Result: rawjson.Message(`{"answer":"yes"}`)}}, nil
		})},
		{name: "pause", pause: true, responder: agenttest.Clarify("yes")},
	}
}

func registerAwaitResume(path awaitResumePath, client model.Client, executed *[]tools.Ident) agenttest.Register {
	return func(ctx context.Context, rt *runtime.Runtime) error {
		if err := rt.RegisterModel("script", client); err != nil {
			return err
		}
		specs := make([]tools.ToolSpec, 0, 3)
		for _, name := range []tools.Ident{"resume.first", "resume.second", "resume.external"} {
			spec := tools.ToolSpec{Name: name, Toolset: "resume", Payload: tools.TypeSpec{Name: "payload", Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Name: "result", Codec: tools.AnyJSONCodec}}
			specs = append(specs, spec)
		}
		if path.confirmation {
			specs[0].Confirmation = &tools.ConfirmationSpec{PromptTemplate: "execute?", DeniedResultTemplate: `{"denied":true}`}
		}
		if err := rt.RegisterToolset(runtime.ToolsetRegistration{Name: "resume", Specs: specs, Execute: func(_ context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			*executed = append(*executed, call.Name)
			result := runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"done": true}})
			if path.pause && call.Name == "resume.first" {
				result.Pause = &runtime.ToolPause{Clarification: &runtime.ToolPauseClarification{ID: "pause", Question: "continue?"}}
			}
			return result, nil
		}}); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "resume", Planner: awaitResumePlanner{await: path.await, specs: specs}, Specs: specs,
			Workflow: engine.WorkflowDefinition{Name: "resume.workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow}, PlanActivityName: "resume.plan", ResumeActivityName: "resume.resume", ExecuteToolActivity: "resume.tool"})
	}
}

// assertAdoptedToolsActedOn rejects adopted model choices with no result in the
// trajectory, including denied confirmations which have a synthesized result.
func assertAdoptedToolsActedOn(t *testing.T, tr *trajectory.Trajectory) {
	t.Helper()
	results := make(map[string]bool)
	for _, step := range tr.Steps {
		if step.Tool != nil {
			results[step.Tool.ToolCallID] = len(step.Tool.Result) > 0
		}
	}
	for _, step := range tr.Steps {
		if step.Model == nil || step.Model.Class != runrecord.Adopted {
			continue
		}
		for _, call := range step.Model.Chosen {
			assert.True(t, results[call.ID], "adopted call %v chose %s but its result was discarded", step.Model.Key, call.ID)
		}
	}
}

func (p awaitResumePlanner) complete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("script")
	if !ok {
		return nil, fmt.Errorf("missing scripted model")
	}
	definitions := make([]*model.ToolDefinition, 0, len(p.specs))
	for _, spec := range p.specs {
		definitions = append(definitions, &model.ToolDefinition{Name: spec.Name.String(), InputSchema: map[string]any{"type": "object"}})
	}
	response, err := client.Complete(ctx, &model.Request{Model: "script", Messages: messages, Tools: definitions})
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
