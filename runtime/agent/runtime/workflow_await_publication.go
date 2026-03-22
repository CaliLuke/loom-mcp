package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
)

func (r *Runtime) publishAwaitQueueItem(ctx context.Context, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, it planner.AwaitItem, idx int) error {
	if it.Kind == "" {
		return fmt.Errorf("await item %d missing kind", idx)
	}

	switch it.Kind {
	case planner.AwaitItemKindClarification:
		return r.publishAwaitClarification(ctx, input, base, turnID, it.Clarification, idx)
	case planner.AwaitItemKindQuestions:
		return r.publishAwaitQuestions(ctx, input, base, st, turnID, it.Questions, idx)
	case planner.AwaitItemKindExternalTools:
		return r.publishAwaitExternalTools(ctx, input, base, st, turnID, it.ExternalTools, idx)
	default:
		return fmt.Errorf("unknown await item kind %q", it.Kind)
	}
}

func (r *Runtime) publishAwaitClarification(ctx context.Context, input *RunInput, base *planner.PlanInput, turnID string, c *planner.AwaitClarification, idx int) error {
	if c == nil {
		return fmt.Errorf("await clarification item %d missing payload", idx)
	}
	return r.publishHook(ctx, hooks.NewAwaitClarificationEvent(
		base.RunContext.RunID,
		input.AgentID,
		base.RunContext.SessionID,
		c.ID,
		c.Question,
		c.MissingFields,
		c.RestrictToTool,
		c.ExampleInput,
	), turnID)
}

func (r *Runtime) publishAwaitQuestions(ctx context.Context, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, q *planner.AwaitQuestions, idx int) error {
	if q == nil {
		return fmt.Errorf("await questions item %d missing payload", idx)
	}
	qs := make([]hooks.AwaitQuestion, 0, len(q.Questions))
	for _, qq := range q.Questions {
		qs = append(qs, awaitQuestionHook(qq))
	}
	if err := r.publishHook(ctx, hooks.NewAwaitQuestionsEvent(
		base.RunContext.RunID,
		input.AgentID,
		base.RunContext.SessionID,
		q.ID,
		q.ToolName,
		q.ToolCallID,
		q.Payload,
		q.Title,
		qs,
	), turnID); err != nil {
		return err
	}
	call := planner.ToolRequest{Name: q.ToolName, ToolCallID: q.ToolCallID, Payload: q.Payload}
	r.recordAssistantTurn(base, st.Transcript, []planner.ToolRequest{call}, st.Ledger)
	if q.ToolCallID == "" {
		return errors.New("await_questions: missing tool_call_id")
	}
	return r.publishHook(ctx, hooks.NewToolCallScheduledEvent(
		base.RunContext.RunID,
		input.AgentID,
		base.RunContext.SessionID,
		q.ToolName,
		q.ToolCallID,
		q.Payload,
		"",
		"",
		0,
	), turnID)
}

func awaitQuestionHook(question planner.AwaitQuestion) hooks.AwaitQuestion {
	opts := make([]hooks.AwaitQuestionOption, 0, len(question.Options))
	for _, o := range question.Options {
		opts = append(opts, hooks.AwaitQuestionOption{ID: o.ID, Label: o.Label})
	}
	return hooks.AwaitQuestion{
		ID:            question.ID,
		Prompt:        question.Prompt,
		AllowMultiple: question.AllowMultiple,
		Options:       opts,
	}
}

func (r *Runtime) publishAwaitExternalTools(ctx context.Context, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, e *planner.AwaitExternalTools, idx int) error {
	if e == nil {
		return fmt.Errorf("await external_tools item %d missing payload", idx)
	}
	if len(e.Items) == 0 {
		return errors.New("await_external_tools: no items in await")
	}
	items, awaitCalls := awaitExternalToolHooks(e.Items)
	if err := r.publishHook(ctx, hooks.NewAwaitExternalToolsEvent(
		base.RunContext.RunID,
		input.AgentID,
		base.RunContext.SessionID,
		e.ID,
		items,
	), turnID); err != nil {
		return err
	}
	r.recordAssistantTurn(base, st.Transcript, awaitCalls, st.Ledger)
	return publishAwaitScheduledToolCalls(ctx, r, input, base, turnID, awaitCalls)
}

func awaitExternalToolHooks(items []planner.AwaitToolItem) ([]hooks.AwaitToolItem, []planner.ToolRequest) {
	hookItems := make([]hooks.AwaitToolItem, 0, len(items))
	awaitCalls := make([]planner.ToolRequest, 0, len(items))
	for _, item := range items {
		hookItems = append(hookItems, hooks.AwaitToolItem{
			ToolName:   item.Name,
			ToolCallID: item.ToolCallID,
			Payload:    item.Payload,
		})
		awaitCalls = append(awaitCalls, planner.ToolRequest{
			Name:       item.Name,
			ToolCallID: item.ToolCallID,
			Payload:    item.Payload,
		})
	}
	return hookItems, awaitCalls
}

func publishAwaitScheduledToolCalls(ctx context.Context, r *Runtime, input *RunInput, base *planner.PlanInput, turnID string, calls []planner.ToolRequest) error {
	for _, call := range calls {
		if call.ToolCallID == "" {
			continue
		}
		if err := r.publishHook(ctx, hooks.NewToolCallScheduledEvent(
			base.RunContext.RunID,
			input.AgentID,
			base.RunContext.SessionID,
			call.Name,
			call.ToolCallID,
			call.Payload,
			"",
			"",
			0,
		), turnID); err != nil {
			return err
		}
	}
	return nil
}
