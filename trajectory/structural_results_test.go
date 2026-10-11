package trajectory_test

import (
	"context"
	"encoding/json/v2"
	"errors"
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
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type (
	structuralResultPlanner struct {
		interceptionPlanner
		requestError bool
	}

	// structuralScriptModel uses Script's raw responses so unknown and denied
	// calls reach runtime canonicalization instead of model-client validation.
	structuralScriptModel struct {
		*agenttest.Script
	}

	denyToolPolicy struct{}
)

func TestBuildStructuralResultMetadata(t *testing.T) {
	for _, kind := range []index.StructuralKind{index.UnknownTool, index.PolicyDenial, index.ConfirmationDenial, index.SynthesizedError, index.Cancellation, index.FinalizeResults} {
		for _, referenced := range []bool{false, true} {
			name := string(kind) + "/inline"
			if referenced {
				name = string(kind) + "/referenced"
			}
			t.Run(name, func(t *testing.T) {
				rec := treeRecord(t)
				entry := &rec.Runs[0].Entries[1]
				entry.Kind, entry.Hash = index.Structural, ""
				entry.Structural = &index.StructuralEntry{Kind: kind, ProtocolVersion: index.TemplateProtocolVersion}
				result := rawjson.Message(`{"error":{"message":"denied"},"large":9007199254740993}`)
				if kind == index.UnknownTool || kind == index.ConfirmationDenial || kind == index.SynthesizedError {
					entry.Structural.Text = []index.TextParameter{{Text: string(result)}}
				}
				toolError := planner.NewToolErrorWithKind("denied", planner.ToolErrorKindOutcomeUnknown)
				toolError.Cause = planner.NewToolError("permission missing")
				bounds := &agent.Bounds{Returned: 1, Truncated: true, RefinementHint: "narrow"}
				output := planner.ToolOutput{Name: "agent.child", ToolCallID: "b", Result: result, Error: toolError, Bounds: bounds, Telemetry: &telemetry.ToolTelemetry{DurationMs: 37}}
				if referenced {
					output.Result = rawjson.Message(`{"preview":true}`)
					output.Refs.Result = "full-result"
					rec.Content[output.Refs.Result] = result
				}
				data, err := json.Marshal(output)
				require.NoError(t, err)
				rec.Content[entry.Output] = data

				got, err := trajectory.Build(rec, "run", trajectory.Options{})
				require.NoError(t, err)
				tool := recordSteps(got)[4].Tool
				assert.Equal(t, result, tool.Result)
				assert.Equal(t, toolError, tool.Error)
				assert.Equal(t, bounds, tool.Bounds)
				assert.Equal(t, 37*time.Millisecond, tool.Duration)
				assert.Equal(t, 1, got.Totals.FailedToolCalls)
			})
		}
	}
}

func TestBuildStructuralResultValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*runrecord.RunRecord)
		message string
	}{
		{"unknown call", func(rec *runrecord.RunRecord) {
			rec.Runs[0].Entries[0].Calls = nil
		}, "no matching call"},
		{"duplicate structural result", func(rec *runrecord.RunRecord) {
			duplicate := rec.Runs[0].Entries[1]
			duplicate.ID = 5
			rec.Runs[0].Entries = append(rec.Runs[0].Entries, duplicate)
		}, "duplicate result"},
		{"duplicate normal result", func(rec *runrecord.RunRecord) {
			duplicate := rec.Runs[0].Entries[1]
			duplicate.ID, duplicate.Kind, duplicate.Structural = 5, index.ToolResult, nil
			rec.Runs[0].Entries = append(rec.Runs[0].Entries, duplicate)
		}, "duplicate result"},
		{"name mismatch", func(rec *runrecord.RunRecord) {
			rec.Content[rec.Runs[0].Entries[1].Output] = []byte(`{"Name":"wrong.tool","ToolCallID":"b"}`)
		}, "name mismatch"},
		{"missing result bytes", func(rec *runrecord.RunRecord) {
			rec.Content[rec.Runs[0].Entries[1].Output] = []byte(`{"Name":"agent.child","ToolCallID":"b","Refs":{"Result":"missing"}}`)
		}, "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := treeRecord(t)
			entry := &rec.Runs[0].Entries[1]
			entry.Kind, entry.Hash = index.Structural, ""
			entry.Structural = &index.StructuralEntry{Kind: index.SynthesizedError, ProtocolVersion: index.TemplateProtocolVersion, Text: []index.TextParameter{{Text: "failed"}}}
			test.change(rec)
			_, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.ErrorContains(t, err, test.message)
		})
	}
}

func TestBuildSkipsEntriesWithoutOutput(t *testing.T) {
	for _, kind := range []index.EntryKind{index.Structural, index.ToolResult} {
		t.Run(string(kind), func(t *testing.T) {
			rec := treeRecord(t)
			entry := &rec.Runs[0].Entries[1]
			entry.Kind, entry.Output = kind, ""
			if kind == index.Structural {
				entry.Hash = ""
				entry.Structural = &index.StructuralEntry{Kind: index.FinalizeHint, ProtocolVersion: index.TemplateProtocolVersion}
			}
			got, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.NoError(t, err)
			tool := recordSteps(got)[4].Tool
			assert.Empty(t, tool.Result)
			assert.Nil(t, tool.Error)
		})
	}
}

func TestBuildRealRuntimeStructuralResults(t *testing.T) {
	for _, test := range []struct {
		name         string
		callName     tools.Ident
		resultName   tools.Ident
		message      string
		cause        string
		kind         index.StructuralKind
		requestError bool
	}{
		{name: "tool error", callName: "test.read", resultName: "test.read", message: "tool activity failed", cause: "executor failed", kind: index.SynthesizedError},
		{name: "inline tool error", callName: "test.read", resultName: "test.read", message: "inline failed", kind: index.SynthesizedError},
		{name: "request error", callName: "test.read", resultName: "test.read", message: "invalid arguments", kind: index.SynthesizedError, requestError: true},
		{name: "unknown tool", callName: "not.registered", resultName: tools.ToolUnavailable, message: "model requested a tool that is unavailable for this run", kind: index.SynthesizedError},
		{name: "policy denial", callName: "test.read", resultName: tools.ToolUnavailable, message: "model requested a tool that is unavailable for this run", kind: index.SynthesizedError},
		{name: "confirmation denial", callName: "test.read", resultName: "test.read", kind: index.ConfirmationDenial},
		{name: "time budget cancellation", callName: "test.read", resultName: "test.read", message: "canceled: time budget reached", kind: index.SynthesizedError},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			turns := []agenttest.Turn{
				agenttest.Respond(agenttest.RawToolCall(test.callName, rawjson.Message(`{}`)), agenttest.CallID("call")),
				agenttest.Respond(agenttest.Text("done")),
			}
			script, err := agenttest.NewScript(turns...)
			require.NoError(t, err)
			bus := hooks.NewBus()
			awaiting := make(chan *hooks.AwaitConfirmationEvent, 1)
			subscription, err := bus.Register(hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
				if await, ok := event.(*hooks.AwaitConfirmationEvent); ok {
					awaiting <- await
				}
				if event.Type() == hooks.ToolCallScheduled && test.name == "time budget cancellation" {
					// Let the workflow timer expire before dispatch, avoiding a race
					// between activity timeout and runtime cancellation synthesis.
					timer := time.NewTimer(300 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}))
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, subscription.Close())
			})
			runtimeOptions := []runtime.RuntimeOption{runtime.WithEngine(engineinmem.New()), runtime.WithHooks(bus)}
			if test.name == "policy denial" {
				runtimeOptions = append(runtimeOptions, runtime.WithPolicy(denyToolPolicy{}))
			}
			if test.name == "confirmation denial" {
				runtimeOptions = append(runtimeOptions, runtime.WithToolConfirmation(&runtime.ToolConfirmationConfig{Confirm: map[tools.Ident]*runtime.ToolConfirmation{
					"test.read": {
						Prompt: func(context.Context, *planner.ToolRequest) (string, error) {
							return "confirm", nil
						},
						DeniedResult: func(context.Context, *planner.ToolRequest) (any, error) {
							return map[string]bool{"denied": true}, nil
						},
					},
				}}))
			}
			rt := runtime.New(runtimeOptions...)
			spec := tools.ToolSpec{Name: "test.read", Toolset: "test", Payload: tools.TypeSpec{Name: "payload", Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Name: "result", Codec: tools.AnyJSONCodec}}
			specs := []tools.ToolSpec{spec}
			if test.name == "policy denial" {
				allowed := spec
				allowed.Name = "test.allowed"
				specs = append(specs, allowed)
			}
			client := script.Client()
			if test.name == "unknown tool" || test.name == "policy denial" {
				client = structuralScriptModel{script}
			}
			require.NoError(t, rt.RegisterModel("registered", client))
			require.NoError(t, rt.RegisterToolset(runtime.ToolsetRegistration{Name: "test", Inline: test.name == "inline tool error", Specs: specs, Execute: func(ctx context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
				if test.name == "time budget cancellation" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if test.name == "inline tool error" {
					return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError("inline failed")}), nil
				}
				if test.name != "tool error" {
					t.Error("denied or invalid call must not execute")
				}
				return nil, errors.New("executor failed")
			}}))
			require.NoError(t, rt.RegisterAgent(ctx, runtime.AgentRegistration{
				ID: "agent", Planner: structuralResultPlanner{requestError: test.requestError}, Specs: specs,
				Workflow:         engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: rt.ExecuteWorkflow},
				PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool",
			}))
			_, err = rt.CreateSession(ctx, "session")
			require.NoError(t, err)
			options := []runtime.RunOption{runtime.WithRunID("run")}
			if test.name == "time budget cancellation" {
				options = append(options, runtime.WithRunTimeBudget(200*time.Millisecond))
			}
			handle, err := rt.MustClient("agent").Start(ctx, "session", nil, options...)
			require.NoError(t, err)
			if test.name == "confirmation denial" {
				select {
				case await := <-awaiting:
					require.NoError(t, rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{RunID: "run", ID: await.ID, Approved: false, RequestedBy: "test"}))
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			_, err = handle.Wait(ctx)
			require.NoError(t, err)
			require.NoError(t, script.Err())
			if test.name == "policy denial" {
				// The requested registered tool was removed from the model catalog.
				request := script.Requests()[0]
				require.Len(t, request.Tools, 1)
				assert.Equal(t, tools.ToolUnavailable.String(), request.Tools[0].Name)
			}
			rec, err := runrecord.Read(ctx, rt.RunStore, rt.RunEventStore, "run", "run")
			require.NoError(t, err)
			outputs := make([]index.MessageEntry, 0, 1)
			for _, entry := range rec.Runs[0].Entries {
				if entry.Output != "" {
					outputs = append(outputs, entry)
				}
			}
			require.Len(t, outputs, 1)
			assert.Equal(t, index.Structural, outputs[0].Kind)
			assert.Equal(t, test.kind, outputs[0].Structural.Kind)
			var output planner.ToolOutput
			require.NoError(t, json.Unmarshal(rec.Content[outputs[0].Output], &output))
			got, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.NoError(t, err)
			steps := recordSteps(got)
			require.Len(t, steps, 3)
			tool := steps[1].Tool
			require.NotNil(t, tool)
			assert.Equal(t, test.resultName, tool.Name)
			assert.Equal(t, output.Error, tool.Error)
			if test.message != "" {
				require.NotNil(t, tool.Error)
				assert.Equal(t, test.message, tool.Error.Message)
				assert.Equal(t, 1, got.Totals.FailedToolCalls)
			} else {
				assert.Nil(t, tool.Error)
				assert.JSONEq(t, `{"denied":true}`, string(tool.Result))
				assert.Zero(t, got.Totals.FailedToolCalls)
			}
			if test.cause != "" {
				require.NotNil(t, tool.Error.Cause)
				assert.Equal(t, test.cause, tool.Error.Cause.Message)
			}
			if output.Refs.Result != "" {
				assert.Equal(t, rawjson.Message(rec.Content[output.Refs.Result]), tool.Result)
			}
		})
	}
}

func (p structuralResultPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	result, err := p.interceptionPlanner.PlanStart(ctx, input)
	if err != nil {
		return nil, err
	}
	if p.requestError {
		result.ToolCalls[0].Error = planner.NewToolError("invalid arguments")
	}
	return result, nil
}

func (structuralScriptModel) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func (denyToolPolicy) Decide(context.Context, policy.Input) (policy.Decision, error) {
	return policy.Decision{AllowedTools: []tools.Ident{"test.allowed"}}, nil
}
