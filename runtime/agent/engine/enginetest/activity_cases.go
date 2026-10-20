package enginetest

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func activities() []activityCase {
	return []activityCase{
		genericActivityCase(false), genericActivityCase(true), hookCase(), plannerCase(), toolCase(), finishCase(), contentCase(), dispatchCase(), sealCase(), recallCase(), summaryCase(),
	}
}

func hookCase() activityCase {
	return activityCase{name: "hook", input: &api.HookActivityInput{RunID: testRunID, EventKey: "event", TimestampMS: 123, Payload: rawjson.Message(`{"nested":{"ok":true}}`)},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterHookActivity(ctx, activityName, opts, func(ctx context.Context, in *api.HookActivityInput) error {
				out, err := fn(ctx, in)
				if out != nil {
					return errors.New("hook must have no output")
				}
				return err
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return nil, w.PublishHook(w.Context(), engine.HookActivityCall{Name: activityName, Input: in.(*api.HookActivityInput), Options: opts})
		}}
}

func plannerCase() activityCase {
	return activityCase{name: "planner", input: &api.PlanActivityInput{RunID: testRunID, ToolPolicyActive: true, AllowedTools: []tools.Ident{echoTool}},
		output: &api.PlanActivityOutput{IndexHead: indexRef, ToolCallRefs: []runstore.Hash{"sha256:call"}, AllowedTools: []tools.Ident{echoTool}, Attempt: 1},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterPlannerActivity(ctx, activityName, opts, func(ctx context.Context, in *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
				return typedResult[*api.PlanActivityOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecutePlannerActivity(w.Context(), engine.PlannerActivityCall{Name: activityName, Input: in.(*api.PlanActivityInput), Options: opts})
		}}
}

func toolCase() activityCase {
	return activityCase{name: "tool", input: &api.ToolInput{RunID: testRunID, ToolName: echoTool, ToolCallID: testCallID, Payload: rawjson.Message(`{"nested":[true,"value"]}`), Labels: map[string]string{"route": "local"}},
		output: &api.ToolOutput{Preview: "echo", Payload: rawjson.Message(`{"value":42}`), ServerData: rawjson.Message(`[{"secret":"sidecar"}]`), Refs: tools.ResultRefs{Result: "sha256:result"}},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterExecuteToolActivity(ctx, activityName, opts, func(ctx context.Context, in *api.ToolInput) (*api.ToolOutput, error) {
				return typedResult[*api.ToolOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecuteToolActivity(w.Context(), engine.ToolActivityCall{Name: activityName, Input: in.(*api.ToolInput), Options: opts})
		}}
}

func finishCase() activityCase {
	return activityCase{name: "finish", input: &api.FinishRunInput{RootRunID: rootRunID, ToolEvents: []*api.ToolEvent{{ToolCallID: testCallID, Result: rawjson.Message(`{"ok":true}`)}}},
		output: &api.FinishRunOutput{FinalRef: "sha256:final", ToolEventsRef: "sha256:events", ToolEventCount: 1, ToolSummary: api.ToolEventSummary{FailedCount: 2}},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterFinishRunActivity(ctx, activityName, opts, func(ctx context.Context, in *api.FinishRunInput) (*api.FinishRunOutput, error) {
				return typedResult[*api.FinishRunOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecuteFinishRunActivity(w.Context(), engine.FinishRunActivityCall{Name: activityName, Input: in.(*api.FinishRunInput), Options: opts})
		}}
}

func contentCase() activityCase {
	return activityCase{name: "record_content", input: &api.RecordContentInput{RootRunID: rootRunID, Items: [][]byte{[]byte("first"), {0, 1, 255}}},
		output: &api.RecordContentOutput{Refs: []runstore.Hash{"sha256:first", "sha256:second"}, IndexHead: indexRef},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterRecordContentActivity(ctx, activityName, opts, func(ctx context.Context, in *api.RecordContentInput) (*api.RecordContentOutput, error) {
				return typedResult[*api.RecordContentOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecuteRecordContentActivity(w.Context(), engine.RecordContentActivityCall{Name: activityName, Input: in.(*api.RecordContentInput), Options: opts})
		}}
}

func dispatchCase() activityCase {
	return activityCase{name: "child_dispatch", input: &api.ChildDispatchInput{Call: planner.ToolRequest{ToolCallID: "child-call", Name: "agents.child", Payload: rawjson.Message(`{"text":"nested child"}`)}},
		output: &api.ChildDispatchOutput{IndexHead: indexRef, Error: "rejection payload"},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterChildDispatchActivity(ctx, activityName, opts, func(ctx context.Context, in *api.ChildDispatchInput) (*api.ChildDispatchOutput, error) {
				return typedResult[*api.ChildDispatchOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecuteChildDispatchActivity(w.Context(), engine.ChildDispatchActivityCall{Name: activityName, Input: in.(*api.ChildDispatchInput), Options: opts})
		}}
}

func sealCase() activityCase {
	return activityCase{name: "seal", input: &api.SealRecordInput{RootRunID: rootRunID, RunID: testRunID, ProtocolVersion: api.WorkflowProtocolVersion, IndexHead: indexRef, EventKey: "run_sealed:run"},
		output: &api.SealRecordOutput{IndexHead: "sha256:sealed"},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterSealRecordActivity(ctx, activityName, opts, func(ctx context.Context, in *api.SealRecordInput) (*api.SealRecordOutput, error) {
				return typedResult[*api.SealRecordOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecuteSealRecordActivity(w.Context(), engine.SealRecordActivityCall{Name: activityName, Input: in.(*api.SealRecordInput), Options: opts})
		}}
}

func recallCase() activityCase {
	return activityCase{name: "recall", input: &api.RecallActivityInput{RunID: testRunID, Scope: rootRunID, Head: indexRef, PageBytes: 512, ScanBytes: 8_000_000, Call: planner.ToolRequest{Name: tools.RecallContext, ToolCallID: testCallID, Payload: rawjson.Message(`{"list":{"from":1,"through":3}}`)}},
		output: &api.RecallActivityOutput{Result: &api.ToolOutput{Preview: "context lookup result", Refs: tools.ResultRefs{Result: "sha256:recall"}}, Bytes: 42},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterRecallActivity(ctx, activityName, opts, func(ctx context.Context, in *api.RecallActivityInput) (*api.RecallActivityOutput, error) {
				return typedResult[*api.RecallActivityOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecuteRecallActivity(w.Context(), engine.RecallActivityCall{Name: activityName, Input: in.(*api.RecallActivityInput), Options: opts})
		}}
}

func summaryCase() activityCase {
	return activityCase{name: "summary", input: &api.SummarizeContextInput{RunID: testRunID, Scope: rootRunID, From: 1, To: 4}, output: &api.SummarizeContextOutput{From: 1, To: 4, Kind: "empty", CallKeys: []runstore.CallKey{{RunID: testRunID, Seq: 1, Attempt: 1}}},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return e.RegisterSummarizeContextActivity(ctx, activityName, opts, func(ctx context.Context, in *api.SummarizeContextInput) (*api.SummarizeContextOutput, error) {
				return typedResult[*api.SummarizeContextOutput](fn(ctx, in))
			})
		}, invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			return w.ExecuteSummarizeContextActivity(w.Context(), engine.SummarizeContextActivityCall{Name: activityName, Input: in.(*api.SummarizeContextInput), Options: opts})
		}}
}
