package runtime

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func (r *Runtime) waitAwaitQueueItem(ctx context.Context, ctrl *interrupt.Controller, input *RunInput, base *workflowPlanInput, st *runLoopState, timeout time.Duration, it planner.AwaitItem) ([]*planner.ToolResult, error) {
	switch it.Kind {
	case planner.AwaitItemKindClarification:
		return waitAwaitClarification(ctx, ctrl, base, timeout, it.Clarification)
	case planner.AwaitItemKindQuestions:
		return r.waitAwaitQuestions(ctx, ctrl, input, base, st, timeout, it.Questions)
	case planner.AwaitItemKindExternalTools:
		return r.waitAwaitExternalTools(ctx, ctrl, input, base, st, timeout, it.ExternalTools)
	case planner.AwaitItemKindTypedInput:
		return waitAwaitTypedInput(ctx, ctrl, input, base, timeout, it.TypedInput)
	default:
		return nil, fmt.Errorf("unknown await item kind %q", it.Kind)
	}
}

func (r *Runtime) consumeProvidedToolResults(ctx context.Context, input *RunInput, base *workflowPlanInput, st *runLoopState, rs *api.ToolResultsSet, allowed []planner.ToolRequest, expected map[string]struct{}) ([]*planner.ToolResult, error) {
	if rs == nil {
		return nil, errors.New("await: nil tool results set")
	}
	if len(rs.Results) == 0 {
		return nil, errors.New("await: no tool results provided")
	}

	if _, err := indexProvidedToolResults(rs, expected); err != nil {
		return nil, err
	}
	wf := engine.WorkflowContextFromContext(ctx)
	if wf == nil {
		return nil, errors.New("provided tool results require workflow context")
	}
	out, err := wf.ExecuteRecordContentActivity(ctx, engine.RecordContentActivityCall{
		Name:  RecordContentActivityName(input.AgentID),
		Input: &api.RecordContentInput{RootRunID: base.RunContext.RootRunID, Provided: &api.ProvidedContent{Calls: allowed, Results: rs.Results, RunContext: base.RunContext, AgentID: input.AgentID, First: index.MessageID(len(base.Entries) + 1), CallRefs: base.CallRefs}},
	})
	if err != nil {
		return nil, err
	}
	decoded := make([]*planner.ToolResult, len(out.ToolResults))
	for i, result := range out.ToolResults {
		decoded[i] = &planner.ToolResult{Name: result.Name, ToolCallID: result.ToolCallID, Refs: result.Refs, Bounds: result.Bounds, Error: result.Error, RetryHint: result.RetryHint, Preview: result.Preview, ResultBytes: result.ResultBytes, Artifacts: artifactContentsFromRefs(result.Artifacts)}
	}
	if err := adoptResultEntries(base, out.Entries, decoded); err != nil {
		return nil, err
	}
	st.ToolEvents = append(st.ToolEvents, cloneToolResults(decoded)...)
	return decoded, nil
}

func waitAwaitClarification(ctx context.Context, ctrl *interrupt.Controller, base *workflowPlanInput, timeout time.Duration, clarification *planner.AwaitClarification) ([]*planner.ToolResult, error) {
	if clarification == nil {
		return nil, errors.New("await clarification missing payload")
	}
	ans, err := ctrl.WaitProvideClarification(ctx, timeout)
	if err != nil {
		return nil, err
	}
	if ans == nil {
		return nil, errors.New("await clarification: nil answer")
	}
	if clarification.ID != "" && ans.ID != "" && ans.ID != clarification.ID {
		return nil, errors.New("unexpected await ID for clarification")
	}
	return nil, appendClarificationAnswer(base, ans.AnswerRef)
}

func waitAwaitTypedInput(ctx context.Context, ctrl *interrupt.Controller, input *RunInput, base *workflowPlanInput, timeout time.Duration, typedInput *planner.AwaitTypedInput) ([]*planner.ToolResult, error) {
	if typedInput == nil {
		return nil, errors.New("await typed_input missing payload")
	}
	if typedInput.ID == "" {
		return nil, errors.New("await typed_input: missing id")
	}
	if len(typedInput.Schema) == 0 || !jsontext.Value(typedInput.Schema).IsValid() {
		return nil, errors.New("await typed_input: invalid schema")
	}
	ans, err := ctrl.WaitProvideTypedInput(ctx, timeout)
	if err != nil {
		return nil, err
	}
	if ans == nil {
		return nil, errors.New("await typed_input: nil answer")
	}
	if ans.ID != "" && ans.ID != typedInput.ID {
		return nil, errors.New("unexpected await ID for typed_input")
	}
	wf := engine.WorkflowContextFromContext(ctx)
	_, err = wf.ExecuteRecordContentActivity(ctx, engine.RecordContentActivityCall{
		Name:  RecordContentActivityName(input.AgentID),
		Input: &api.RecordContentInput{RootRunID: base.RunContext.RootRunID, Typed: &api.TypedContentValidation{Schema: typedInput.Schema, PayloadRef: ans.PayloadRef}},
	})
	if err != nil {
		return nil, err
	}
	return nil, base.appendEntry(index.MessageEntry{Kind: index.TypedInput, Hash: ans.PayloadRef, InputID: typedInput.ID})
}

func validateTypedInputPayload(schemaJSON rawjson.Message, payload rawjson.Message) error {
	schemaDoc, err := model.DecodeSchemaJSON(schemaJSON.RawMessage())
	if err != nil {
		return fmt.Errorf("await typed_input: invalid schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaDoc); err != nil {
		return fmt.Errorf("await typed_input: invalid schema: %w", err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("await typed_input: invalid schema: %w", err)
	}
	value, err := model.DecodeSchemaJSON(payload.RawMessage())
	if err != nil {
		return fmt.Errorf("await typed_input: invalid answer payload: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("await typed_input: answer does not match schema: %w", err)
	}
	return nil
}

func (r *Runtime) waitAwaitQuestions(ctx context.Context, ctrl *interrupt.Controller, input *RunInput, base *workflowPlanInput, st *runLoopState, timeout time.Duration, questions *planner.AwaitQuestions) ([]*planner.ToolResult, error) {
	if questions == nil {
		return nil, errors.New("await questions missing payload")
	}
	rs, err := waitForProvidedToolResults(ctx, ctrl, timeout, questions.ID, "await questions")
	if err != nil {
		return nil, err
	}
	expected := map[string]struct{}{questions.ToolCallID: {}}
	allowed := []planner.ToolRequest{{
		Name:       questions.ToolName,
		ToolCallID: questions.ToolCallID,
		Payload:    questions.Payload,
	}}
	return r.consumeProvidedToolResults(ctx, input, base, st, rs, allowed, expected)
}

func (r *Runtime) waitAwaitExternalTools(ctx context.Context, ctrl *interrupt.Controller, input *RunInput, base *workflowPlanInput, st *runLoopState, timeout time.Duration, external *planner.AwaitExternalTools) ([]*planner.ToolResult, error) {
	if external == nil {
		return nil, errors.New("await external_tools missing payload")
	}
	rs, err := waitForProvidedToolResults(ctx, ctrl, timeout, external.ID, "await external_tools")
	if err != nil {
		return nil, err
	}
	allowed, expected, err := buildExternalAwaitRequests(external.Items)
	if err != nil {
		return nil, err
	}
	return r.consumeProvidedToolResults(ctx, input, base, st, rs, allowed, expected)
}

func waitForProvidedToolResults(ctx context.Context, ctrl *interrupt.Controller, timeout time.Duration, awaitID string, kind string) (*api.ToolResultsSet, error) {
	rs, err := ctrl.WaitProvideToolResults(ctx, timeout)
	if err != nil {
		return nil, err
	}
	if rs == nil {
		return nil, fmt.Errorf("%s: nil tool results set", kind)
	}
	if awaitID != "" && rs.ID != "" && rs.ID != awaitID {
		return nil, fmt.Errorf("unexpected await ID for %s", strings.TrimPrefix(kind, "await "))
	}
	return rs, nil
}

func buildExternalAwaitRequests(items []planner.AwaitToolItem) ([]planner.ToolRequest, map[string]struct{}, error) {
	expected := make(map[string]struct{}, len(items))
	allowed := make([]planner.ToolRequest, 0, len(items))
	for _, item := range items {
		if item.ToolCallID == "" {
			return nil, nil, fmt.Errorf("await_external_tools: missing tool_call_id for external tool %q", item.Name)
		}
		expected[item.ToolCallID] = struct{}{}
		allowed = append(allowed, planner.ToolRequest{
			Name:       item.Name,
			ToolCallID: item.ToolCallID,
			Payload:    item.Payload,
		})
	}
	return allowed, expected, nil
}

func indexProvidedToolResults(rs *api.ToolResultsSet, expected map[string]struct{}) (map[string]*api.ProvidedToolResult, error) {
	seen := make(map[string]struct{}, len(rs.Results))
	providedByID := make(map[string]*api.ProvidedToolResult, len(rs.Results))
	for _, item := range rs.Results {
		if item == nil {
			return nil, errors.New("await: nil tool result")
		}
		if item.ToolCallID == "" {
			return nil, fmt.Errorf("await: result for tool %q missing tool_call_id", item.Name)
		}
		if expected != nil {
			if _, ok := expected[item.ToolCallID]; !ok {
				return nil, fmt.Errorf("await: unexpected tool result for tool_call_id %q", item.ToolCallID)
			}
		}
		if _, dup := seen[item.ToolCallID]; dup {
			return nil, fmt.Errorf("await: duplicate result for tool_call_id %q", item.ToolCallID)
		}
		seen[item.ToolCallID] = struct{}{}
		providedByID[item.ToolCallID] = item
	}
	if expected != nil && len(seen) != len(expected) {
		return nil, fmt.Errorf("await: tool result ids did not match awaited tool_use ids (awaited=%d, got=%d)", len(expected), len(seen))
	}
	return providedByID, nil
}

func (r *Runtime) publishProvidedToolResults(ctx context.Context, input *RunInput, base *workflowPlanInput, turnID string, decoded []*planner.ToolResult, resultJSONs []rawjson.Message) error {
	for i, tr := range decoded {
		if tr == nil {
			continue
		}
		if err := r.publishProvidedToolResult(ctx, input, base, turnID, tr, resultJSONs, i); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) publishProvidedToolResult(ctx context.Context, input *RunInput, base *workflowPlanInput, turnID string, tr *planner.ToolResult, resultJSONs []rawjson.Message, idx int) error {
	var resultJSON rawjson.Message
	if idx < len(resultJSONs) {
		resultJSON = resultJSONs[idx]
	}
	ev := hooks.NewToolResultReceivedEvent(
		base.RunContext.RunID,
		input.AgentID,
		base.RunContext.SessionID,
		tr.Name,
		tr.ToolCallID,
		"",
		tr.Result,
		resultJSON,
		tr.ServerData,
		r.formatResultPreview(ctx, tr.Name, rawjson.Message(nil), tr.Result, tr.Bounds),
		tr.Bounds,
		0,
		nil,
		tr.RetryHint,
		tr.Error,
	)
	ev.Artifacts = artifactRefsFromContents(tr.Artifacts)
	return r.publishHook(ctx, ev, turnID)
}
