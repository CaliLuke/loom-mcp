package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"goa.design/goa-ai/runtime/agent/api"
	"goa.design/goa-ai/runtime/agent/hooks"
	"goa.design/goa-ai/runtime/agent/interrupt"
	"goa.design/goa-ai/runtime/agent/model"
	"goa.design/goa-ai/runtime/agent/planner"
	"goa.design/goa-ai/runtime/agent/rawjson"
)

func (r *Runtime) waitAwaitQueueItem(ctx context.Context, ctrl *interrupt.Controller, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, timeout time.Duration, it planner.AwaitItem) ([]*planner.ToolResult, error) {
	switch it.Kind {
	case planner.AwaitItemKindClarification:
		return waitAwaitClarification(ctx, ctrl, base, timeout, it.Clarification)
	case planner.AwaitItemKindQuestions:
		return r.waitAwaitQuestions(ctx, ctrl, input, base, st, turnID, timeout, it.Questions)
	case planner.AwaitItemKindExternalTools:
		return r.waitAwaitExternalTools(ctx, ctrl, input, base, st, turnID, timeout, it.ExternalTools)
	default:
		return nil, fmt.Errorf("unknown await item kind %q", it.Kind)
	}
}

func (r *Runtime) consumeProvidedToolResults(ctx context.Context, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, rs *api.ToolResultsSet, allowed []planner.ToolRequest, expected map[string]struct{}) ([]*planner.ToolResult, error) {
	if rs == nil {
		return nil, errors.New("await: nil tool results set")
	}
	if len(rs.Results) == 0 {
		return nil, errors.New("await: no tool results provided")
	}

	providedByID, err := indexProvidedToolResults(rs, expected)
	if err != nil {
		return nil, err
	}

	decoded, resultJSONs, err := r.decodeProvidedToolResults(ctx, allowed, providedByID)
	if err != nil {
		return nil, err
	}

	if err := r.recordProvidedToolResults(ctx, base, st, allowed, decoded); err != nil {
		return nil, err
	}
	if err := r.publishProvidedToolResults(ctx, input, base, turnID, decoded, resultJSONs); err != nil {
		return nil, err
	}
	return decoded, nil
}

func waitAwaitClarification(ctx context.Context, ctrl *interrupt.Controller, base *planner.PlanInput, timeout time.Duration, clarification *planner.AwaitClarification) ([]*planner.ToolResult, error) {
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
	if ans.Answer != "" {
		base.Messages = append(base.Messages, &model.Message{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: ans.Answer}},
		})
	}
	return nil, nil
}

func (r *Runtime) waitAwaitQuestions(ctx context.Context, ctrl *interrupt.Controller, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, timeout time.Duration, questions *planner.AwaitQuestions) ([]*planner.ToolResult, error) {
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
	return r.consumeProvidedToolResults(ctx, input, base, st, turnID, rs, allowed, expected)
}

func (r *Runtime) waitAwaitExternalTools(ctx context.Context, ctrl *interrupt.Controller, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, timeout time.Duration, external *planner.AwaitExternalTools) ([]*planner.ToolResult, error) {
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
	return r.consumeProvidedToolResults(ctx, input, base, st, turnID, rs, allowed, expected)
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

func (r *Runtime) recordProvidedToolResults(ctx context.Context, base *planner.PlanInput, st *runLoopState, allowed []planner.ToolRequest, decoded []*planner.ToolResult) error {
	st.ToolEvents = append(st.ToolEvents, cloneToolResults(decoded)...)
	if err := r.appendToolOutputs(ctx, st, allowed, decoded); err != nil {
		return err
	}
	return r.appendUserToolResults(base, allowed, decoded, st.Ledger)
}

func (r *Runtime) publishProvidedToolResults(ctx context.Context, input *RunInput, base *planner.PlanInput, turnID string, decoded []*planner.ToolResult, resultJSONs []rawjson.Message) error {
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

func (r *Runtime) publishProvidedToolResult(ctx context.Context, input *RunInput, base *planner.PlanInput, turnID string, tr *planner.ToolResult, resultJSONs []rawjson.Message, idx int) error {
	var resultJSON rawjson.Message
	if idx < len(resultJSONs) {
		resultJSON = resultJSONs[idx]
	}
	return r.publishHook(
		ctx,
		hooks.NewToolResultReceivedEvent(
			base.RunContext.RunID,
			input.AgentID,
			base.RunContext.SessionID,
			tr.Name,
			tr.ToolCallID,
			"",
			tr.Result,
			resultJSON,
			tr.ServerData,
			formatResultPreview(tr.Name, tr.Result, tr.Bounds),
			tr.Bounds,
			0,
			nil,
			tr.RetryHint,
			tr.Error,
		),
		turnID,
	)
}
