package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

type recallExecution struct {
	result *planner.ToolResult
	json   rawjson.Message
	code   recall.Code
	bytes  int
}

func (e *toolBatchExec) dispatchRecall(ctx context.Context, wf engine.WorkflowContext, b *toolCallBatch, call planner.ToolRequest) error {
	charge, ok := e.base.recallCharges[call.ToolCallID]
	if !ok {
		return fmt.Errorf("recall: call %s dispatched before charging", call.ToolCallID)
	}
	execution, err := e.executeRecall(ctx, wf, call, charge)
	if err != nil {
		return err
	}
	result, resultJSON, code, bytes := execution.result, execution.json, execution.code, execution.bytes

	event := hooks.NewContextRecalledEvent(e.runID, e.agentID, e.sessionID, call.ToolCallID, wf.Now())
	event.Kind, event.Code, event.Bytes, event.Step = charge.kind, code, bytes, e.base.step
	if result.Refs.Parts != "" {
		event.Parts = 1
	}
	event.RemainingDiscovery, event.RemainingReads = charge.remaining.Discovery, charge.remaining.Reads
	event.Outcome = "ok"
	switch code {
	case recall.CodeBudgetExhausted:
		event.Outcome = "exhausted"
	case recall.CodeUnsupportedPart:
		event.Outcome = "unsupported"
	case "":
	case recall.CodeNotFound, recall.CodeUnauthorized, recall.CodeUnavailable, recall.CodeInvalidInput:
		event.Outcome = "rejected"
	}
	if err := e.r.publishHook(ctx, event, e.turnID); err != nil {
		return err
	}
	if err := e.publishToolResultReceived(ctx, call, result, resultJSON, 0); err != nil {
		return err
	}
	b.inlineByID[call.ToolCallID] = Executed(result)
	e.recordDiscoveredToolCall(b, call.ToolCallID)
	return nil
}

func (e *toolBatchExec) executeRecall(ctx context.Context, wf engine.WorkflowContext, call planner.ToolRequest, charge recallCharge) (recallExecution, error) {
	if charge.exhausted {
		return e.exhaustedRecall(ctx, wf, call, charge)
	}
	var result *planner.ToolResult
	var code recall.Code
	bytes := 0

	pageBytes := 512
	if e.base.compaction != nil {
		pageBytes = e.base.contextSettings.RecallPageBytes(e.base.compactionState.MinB)
	}
	output, err := wf.ExecuteRecallActivity(ctx, engine.RecallActivityCall{Name: RecallActivityName(e.agentID), Input: &api.RecallActivityInput{Parts: compaction.ToolResultParts(e.base.compactionState), PartTokens: e.base.contextSettings.RecallPartTokens(e.base.compactionState.MinB), RunID: e.runID, Scope: e.base.RunContext.RootRunID, ViewRef: e.base.view(), Head: e.base.IndexHead, Call: call, PageBytes: pageBytes, ScanBytes: e.base.contextSettings.RecallScanBytes}, Options: computeToolActivityOptions(wf, e.toolActOptions, e.finishBy)})
	if err != nil {
		return recallExecution{}, err
	}
	if output == nil || output.Result == nil {
		return recallExecution{}, fmt.Errorf("recall: missing activity output")
	}
	result, err = e.decodeActivityToolResult(futureInfo{call: call}, output.Result)
	if err != nil {
		return recallExecution{}, err
	}
	code, bytes = output.Code, output.Bytes
	return recallExecution{result: result, code: code, bytes: bytes}, nil
}

func (e *toolBatchExec) exhaustedRecall(ctx context.Context, wf engine.WorkflowContext, call planner.ToolRequest, charge recallCharge) (recallExecution, error) {
	var result *planner.ToolResult
	var resultJSON rawjson.Message
	var code recall.Code
	bytes := 0

	allowance := "discovery"
	if charge.kind == recall.Read {
		allowance = "reads"
	}
	payload := struct {
		Code           recall.Code `json:"code"`
		Allowance      string      `json:"allowance"`
		OtherRemaining int         `json:"other_remaining"`
		RefillsAt      string      `json:"refills_at"`
	}{recall.CodeBudgetExhausted, allowance, charge.otherRemaining, "next compaction"}
	data, err := json.Marshal(payload, json.Deterministic(true))
	if err != nil {
		return recallExecution{}, err
	}
	resultJSON = rawjson.Message(data)
	result = &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: payload, Preview: "context lookup allowance exhausted", ResultBytes: len(data)}
	if err := e.r.recordWorkflowToolResult(ctx, result, resultJSON); err != nil {
		return recallExecution{}, err
	}
	code, bytes = recall.CodeBudgetExhausted, len(data)
	if e.base.recallExhausted == nil {
		e.base.recallExhausted = map[bool]bool{}
	}
	read := charge.kind == recall.Read
	if !e.base.recallExhausted[read] {
		event := hooks.NewRecallBudgetExhaustedEvent(e.runID, e.agentID, e.sessionID, allowance, e.base.recallEpisode, wf.Now())
		if err := e.r.publishHook(ctx, event, e.turnID); err != nil {
			return recallExecution{}, err
		}
		e.base.recallExhausted[read] = true
	}
	return recallExecution{result: result, json: resultJSON, code: code, bytes: bytes}, nil
}

// prepareRecallDispatch keeps construction failures on the charged lookup path.
func (e *toolBatchExec) prepareRecallDispatch(ctx context.Context, b *toolCallBatch, call planner.ToolRequest) (*planner.ToolRequest, error) {
	if e.base == nil || !e.base.recallAvailable {
		if err := e.dispatchUnknownToolCall(ctx, b, call); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err := e.publishToolCallScheduled(ctx, call, e.toolQueue(call.Name)); err != nil {
		return nil, err
	}
	return &call, nil
}
