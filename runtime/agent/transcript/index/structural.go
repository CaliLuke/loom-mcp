package index

import (
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// appendStructural interprets protocol 5 templates. validateStructural rejects
// other versions before any content read; future protocols need explicit cases.
func (r *Renderer) appendStructural(entry MessageEntry) error {
	s := entry.Structural
	switch s.Kind {
	case CarriedResult:
		text, err := r.text(s.Text[0])
		if err != nil {
			return err
		}
		if len(entry.ToolUseIDs) != 1 || entry.Batch == 0 {
			return fmt.Errorf("carried result requires one tool use ID and a batch")
		}
		r.batch = entry.Batch
		r.parts = append(r.parts, model.ToolResultPart{ToolUseID: entry.ToolUseIDs[0], Content: text, IsError: entry.Result != nil && entry.Result.Error != nil})
	case HideBookkeeping:
		r.hideCalls(entry.ToolUseIDs)
	case Reminders:
		return r.appendReminders(s.Text)
	case FinalizeResults:
		r.prepareFinalize()
	case FinalizeHint:
		r.appendText(model.ConversationRoleSystem, finalizeHint(s.Reason))
	case PolicyDenial:
		if len(entry.ToolUseIDs) == 0 {
			return nil // Policy filtering does not currently add a message.
		}
		return r.appendStructuralResult(entry)
	case UnknownTool, ConfirmationDenial, SynthesizedError, Cancellation:
		return r.appendStructuralResult(entry)
	}
	return nil
}

func (r *Renderer) appendStructuralResult(entry MessageEntry) error {
	s := entry.Structural
	var text string
	if len(s.Text) > 0 {
		var err error
		text, err = r.text(s.Text[0])
		if err != nil {
			return err
		}
	}
	if s.Kind == ConfirmationDenial {
		return r.appendResult(entry, []byte(text))
	}
	if s.Kind == UnknownTool {
		text = fmt.Sprintf("unknown tool %q", text)
	}
	if s.Kind == Cancellation {
		text = "canceled: time budget reached"
	}
	projection := ResultProjection{}
	if entry.Result != nil {
		projection = *entry.Result
	}
	projection.Error = &TextParameter{Text: text}
	entry.Result = &projection
	return r.appendResult(entry, nil)
}

func (r *Renderer) appendReminders(params []TextParameter) error {
	if len(params) == 0 {
		return nil
	}
	var text strings.Builder
	for i, param := range params {
		value, err := r.text(param)
		if err != nil {
			return err
		}
		if i > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString("<system-reminder>")
		text.WriteString(value)
		text.WriteString("</system-reminder>")
	}
	r.appendText(model.ConversationRoleSystem, text.String())
	return nil
}

// hideCalls matches the existing backward scan, including its handling of
// duplicate IDs and removal of emptied assistant messages encountered en route.
func (r *Renderer) hideCalls(ids []string) {
	remaining := make(map[string]bool, len(ids))
	for _, id := range ids {
		remaining[id] = true
		r.hidden[id] = true
	}
	if len(remaining) == 0 {
		return
	}
	for i := len(r.messages) - 1; i >= 0; i-- {
		msg := r.messages[i]
		if msg == nil || msg.Role != model.ConversationRoleAssistant {
			continue
		}
		parts := msg.Parts[:0]
		for _, part := range msg.Parts {
			if call, ok := part.(model.ToolUsePart); ok && remaining[call.ID] {
				delete(remaining, call.ID)
				continue
			}
			parts = append(parts, part)
		}
		msg.Parts = parts
		if len(parts) == 0 {
			r.messages = append(r.messages[:i], r.messages[i+1:]...)
		}
		if len(remaining) == 0 {
			return
		}
	}
}

func (r *Renderer) prepareFinalize() {
	// prepareFinalizationMessages clones first, dropping nil messages and
	// normalizing empty metadata/parts even when there are no outstanding calls.
	out := make([]*model.Message, 0, len(r.messages))
	for _, msg := range r.messages {
		if msg == nil {
			continue
		}
		if msg.Parts == nil {
			msg.Parts = []model.Part{}
		}
		if len(msg.Meta) == 0 {
			msg.Meta = nil
		}
		out = append(out, msg)
	}
	if len(r.messages) == 0 {
		r.messages = nil
		return
	}
	r.messages = out
	if len(out) == 0 || out[len(out)-1].Role != model.ConversationRoleAssistant {
		return
	}
	var parts []model.Part
	for _, part := range out[len(out)-1].Parts {
		if call, ok := part.(model.ToolUsePart); ok {
			parts = append(parts, model.ToolResultPart{
				ToolUseID: call.ID, IsError: true,
				Content: "Finalized before a tool result was provided for this request.",
			})
		}
	}
	if len(parts) > 0 {
		r.messages = append(r.messages, &model.Message{Role: model.ConversationRoleUser, Parts: parts})
	}
}

func finalizeHint(reason string) string {
	const guidance = "\n- Do NOT call domain tools. You may call an advertised terminal bookkeeping tool to produce the final result."
	switch reason {
	case "time_budget":
		return "FINALIZE NOW: time budget reached.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + guidance + "\n- Do NOT say you will call tools or that you will \"try\" another approach.\n- If additional domain tool calls would be needed, explain what you would have retrieved and how it would change the answer, then provide the best provisional answer."
	case "tool_cap":
		return "FINALIZE NOW: tool budget exhausted.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + guidance + "\n- Do NOT say you will call tools.\n- If further domain tool calls would be needed, describe them briefly and provide the best provisional answer."
	case "failure_cap":
		return "FINALIZE NOW: recovery budget exhausted.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + guidance + "\n- Do NOT say you will call tools.\n- A model or tool result could not be accepted after bounded retries. State what remains uncertain, then provide the best provisional answer."
	case "outcome_unknown":
		return "FINALIZE NOW: a tool execution outcome is unknown.\n\n- Do NOT call any tools or issue a replacement call. The original effect may already have occurred.\n- Use only the information already available in the conversation and tool results. State clearly what remains uncertain."
	default:
		return "FINALIZE NOW.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + guidance + "\n- Do NOT say you will call tools.\n- If more work is needed, describe it succinctly and provide the best provisional answer."
	}
}
