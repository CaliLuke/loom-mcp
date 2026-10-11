package expect_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
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

type scriptPlanner struct {
	modelID     string
	definitions []*model.ToolDefinition
	await       *planner.Await
	abort       bool
}

type runtimeScenario struct {
	turns []agenttest.Turn
	child bool
	await *planner.Await
	abort bool
}

func recordedTrajectory(t *testing.T, scenario runtimeScenario) *trajectory.Trajectory {
	t.Helper()
	got, err := trajectory.Build(recordedRun(t, scenario), "run", trajectory.Options{})
	require.NoError(t, err)
	return got
}

func recordedRun(t *testing.T, scenario runtimeScenario) *runrecord.RunRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	bus := hooks.NewBus()
	events := make(chan hooks.Event, 16)
	subscription, err := bus.Register(hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		switch event.Type() {
		case hooks.AwaitClarification, hooks.AwaitQuestions, hooks.AwaitExternalTools, hooks.AwaitTypedInput:
			select {
			case events <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		case hooks.RunStarted, hooks.RunCompleted, hooks.RunPaused,
			hooks.AwaitConfirmation, hooks.ToolAuthorization, hooks.RunResumed,
			hooks.ToolCallScheduled, hooks.ToolCallArgsDelta, hooks.ToolResultReceived,
			hooks.ToolCallUpdated, hooks.PlannerNote, hooks.ThinkingBlock,
			hooks.AssistantMessage, hooks.AssistantTurnCommitted, hooks.RetryHintIssued,
			hooks.MemoryAppended, hooks.PolicyDecision, hooks.Usage,
			hooks.HardProtectionTriggered, hooks.RunPhaseChanged, hooks.ChildRunLinked,
			hooks.PromptRendered, hooks.PlannerStep, hooks.RunSealed,
			hooks.ContextCompacted, hooks.ContextCompactionSkipped,
			hooks.ContextOverflow, hooks.ContextRequestGrowth,
			hooks.ContextUnmeasured, hooks.ContextLimitsUnknown, hooks.ContextBudgetTooSmall:
			// The fixture answers only the four planner AwaitItem kinds.
			return nil
		}
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, subscription.Close())
	})
	rt := runtime.New(runtime.WithEngine(engineinmem.New()), runtime.WithHooks(bus))
	spec := tools.ToolSpec{Name: "echo.echo", Toolset: "echo", Bounds: &tools.BoundsSpec{}, Payload: tools.TypeSpec{Name: "payload", Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Name: "result", Codec: tools.AnyJSONCodec}}
	require.NoError(t, rt.RegisterToolset(runtime.ToolsetRegistration{Name: "echo", Inline: true, Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
		var payload echoPayload
		if err := json.Unmarshal(call.Payload, &payload); err != nil {
			return nil, err
		}
		result := &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID}
		if payload.Value == "fail" {
			result.Error = planner.NewToolError("boom")
		} else {
			result.Result = &echoResult{Value: payload.Value + "!"}
			result.Bounds = &agent.Bounds{Returned: 1, Truncated: true, RefinementHint: "narrow"}
		}
		return runtime.Executed(result), nil
	}}))
	specs := []tools.ToolSpec{spec}
	if scenario.child {
		registerScriptAgent(t, rt, "child", []tools.ToolSpec{spec}, runtimeScenario{turns: []agenttest.Turn{toolTurn("echo.echo", `{"value":"child"}`), finalTurn()}})
		childSpec := tools.ToolSpec{Name: "child.run", Toolset: "child", IsAgentTool: true, AgentID: "child", Payload: tools.TypeSpec{Name: "payload", Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Name: "result", Codec: tools.AnyJSONCodec}}
		registration := runtime.NewAgentToolsetRegistration(rt, runtime.AgentToolConfig{Name: "child", AgentID: "child", Route: runtime.AgentRoute{ID: "child", WorkflowName: "child.workflow", DefaultTaskQueue: "queue"}})
		registration.Specs = []tools.ToolSpec{childSpec}
		require.NoError(t, rt.RegisterToolset(registration))
		specs = append(specs, childSpec)
	}
	registerScriptAgent(t, rt, "root", specs, scenario)
	_, err = rt.CreateSession(ctx, "session")
	require.NoError(t, err)
	handle, err := rt.MustClient("root").Start(ctx, "session", nil, runtime.WithRunID("run"))
	require.NoError(t, err)
	if scenario.await != nil {
		for range scenario.await.Items {
			select {
			case event := <-events:
				require.NoError(t, answerAwait(ctx, rt, event))
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	_, err = handle.Wait(ctx)
	if scenario.abort {
		require.ErrorContains(t, err, "abort after model")
	} else {
		require.NoError(t, err)
	}
	record, err := runrecord.Read(ctx, rt.RunStore, rt.RunEventStore, "run", "run")
	require.NoError(t, err)
	return record
}

func registerScriptAgent(t *testing.T, rt *runtime.Runtime, id agent.Ident, specs []tools.ToolSpec, scenario runtimeScenario) {
	t.Helper()
	client, script := agenttest.ScriptClient(t, scenario.turns...)
	require.NoError(t, rt.RegisterModel(string(id), client))
	definitions := make([]*model.ToolDefinition, len(specs))
	for i, spec := range specs {
		definitions[i] = &model.ToolDefinition{Name: spec.Name.String(), InputSchema: map[string]any{"type": "object"}}
	}
	require.NoError(t, rt.RegisterAgent(t.Context(), runtime.AgentRegistration{
		ID: id, Planner: scriptPlanner{modelID: string(id), definitions: definitions, await: scenario.await, abort: scenario.abort}, Specs: specs,
		Workflow:         engine.WorkflowDefinition{Name: string(id) + ".workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: string(id) + ".plan", ResumeActivityName: string(id) + ".resume", ExecuteToolActivity: string(id) + ".tool",
	}))
	t.Cleanup(func() {
		assert.NoError(t, script.Err())
	})
}

func toolTurn(name tools.Ident, payload string) agenttest.Turn {
	return agenttest.Respond(agenttest.RawToolCall(name, rawjson.Message(payload)), agenttest.Usage(3, 2))
}

func finalTurn() agenttest.Turn {
	return agenttest.Respond(agenttest.Text("done"), agenttest.Usage(3, 2))
}

func runtimeEchoTool() tools.TypedTool[*echoPayload, *echoResult] {
	tool := echoTool()
	tool.Result.FromJSON = func(data []byte) (*echoResult, error) {
		var result echoResult
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return &result, nil
	}
	return tool
}

func answerAwait(ctx context.Context, rt *runtime.Runtime, event hooks.Event) error {
	switch await := event.(type) {
	case *hooks.AwaitClarificationEvent:
		return rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run", ID: await.ID, Answer: "yes"})
	case *hooks.AwaitTypedInputEvent:
		return rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{RunID: "run", ID: await.ID, Payload: rawjson.Message(`{"value":"yes"}`)})
	case *hooks.AwaitQuestionsEvent:
		return rt.ProvideToolResults(ctx, &api.ToolResultsSet{RunID: "run", ID: await.ID, Results: []*api.ProvidedToolResult{{Name: await.ToolName, ToolCallID: await.ToolCallID, Result: rawjson.Message(`{"value":"yes"}`), Bounds: &agent.Bounds{Returned: 1}}}})
	case *hooks.AwaitExternalToolsEvent:
		return rt.ProvideToolResults(ctx, &api.ToolResultsSet{RunID: "run", ID: await.ID, Results: []*api.ProvidedToolResult{{Name: await.Items[0].ToolName, ToolCallID: await.Items[0].ToolCallID, Result: rawjson.Message(`{"value":"yes"}`), Bounds: &agent.Bounds{Returned: 1}}}})
	default:
		return fmt.Errorf("unexpected await %s", event.Type())
	}
}

func (p scriptPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	result, err := p.complete(ctx, input.Agent, input.Messages)
	if err != nil {
		return nil, err
	}
	if p.abort {
		return nil, errors.New("abort after model")
	}
	if p.await != nil {
		return &planner.PlanResult{Await: p.await}, nil
	}
	return result, nil
}

func (p scriptPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, input.Agent, input.Messages)
}

func (p scriptPlanner) complete(ctx context.Context, agent planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := agent.ModelClient(p.modelID)
	if !ok {
		return nil, errors.New("missing script model")
	}
	response, err := client.Complete(ctx, &model.Request{Model: "scripted", Messages: messages, Tools: p.definitions})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{}
	for _, call := range response.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: call.Name, ToolCallID: call.ID, Payload: call.Payload})
	}
	if len(result.ToolCalls) == 0 {
		result.FinalResponse = &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}
	}
	return result, nil
}
