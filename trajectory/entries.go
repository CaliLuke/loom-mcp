package trajectory

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// readEntries consumes the next canonical prefix read by a planner step. Input
// answers belong to that reader; assistant entries belong to their producing call.
// The terminal seal's trailing entries are consumed after the last boundary.
func (r *runBuilder) readEntries(step int, last index.MessageID) error {
	for r.position < len(r.run.Entries) {
		entry := r.run.Entries[r.position]
		if entry.ID > last {
			break
		}
		if err := r.readEntry(step, entry); err != nil {
			return fmt.Errorf("entry %d: %w", entry.ID, err)
		}
		r.position++
	}
	return nil
}

func (r *runBuilder) readEntry(step int, entry index.MessageEntry) error {
	switch entry.Kind {
	case index.Assistant:
		if entry.Call != nil {
			producingStep, ok := r.callSteps[*entry.Call]
			if !ok {
				return fmt.Errorf("assistant call %v has no matching model call", *entry.Call)
			}
			step = producingStep
		}
		if err := r.readToolCalls(step, entry); err != nil {
			return err
		}
		return r.readFinal(entry)
	case index.ToolResult, index.Structural:
		// Structural results carry the same canonical output metadata as normal
		// results. Successful bookkeeping results and projection-only structural
		// operations are indexed without an output and are not eval steps.
		if entry.Output != "" {
			return r.readToolResult(entry)
		}
	case index.Clarification, index.TypedInput:
		data, err := r.owner.content(entry.Hash)
		if err != nil {
			return err
		}
		if entry.Kind == index.Clarification {
			data, err = json.Marshal(string(data), jsontext.AllowInvalidUTF8(true))
			if err != nil {
				return fmt.Errorf("encode clarification text: %w", err)
			}
		}
		answer := &AnswerStep{Kind: entry.Kind, InputID: entry.InputID, Content: data}
		r.group(step).entries = append(r.group(step).entries, Step{Kind: StepAnswer, Step: step, EntryID: entry.ID, Verified: true, Answer: answer})
	case index.Message:
		// Caller messages are not eval steps.
	}
	return nil
}

func (r *runBuilder) readToolCalls(step int, entry index.MessageEntry) error {
	for _, hash := range entry.Calls {
		data, err := r.owner.content(hash)
		if err != nil {
			return err
		}
		var call planner.ToolRequest
		if err := json.Unmarshal(data, &call); err != nil {
			return fmt.Errorf("decode tool call: %w", err)
		}
		if _, exists := r.tools[call.ToolCallID]; exists {
			return fmt.Errorf("tool call %q called twice in one run", call.ToolCallID)
		}
		tool := &ToolStep{Name: call.Name, ToolCallID: call.ToolCallID, ParentToolCallID: call.ParentToolCallID, Arguments: call.Payload}
		r.tools[call.ToolCallID] = tool
		r.group(step).entries = append(r.group(step).entries, Step{Kind: StepTool, Step: step, EntryID: entry.ID, Verified: true, Tool: tool})
	}
	return nil
}

func (r *runBuilder) readToolResult(entry index.MessageEntry) error {
	data, err := r.owner.content(entry.Output)
	if err != nil {
		return err
	}
	var output planner.ToolOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return fmt.Errorf("decode tool output: %w", err)
	}
	tool, exists := r.tools[output.ToolCallID]
	if !exists {
		return fmt.Errorf("result %q has no matching call", output.ToolCallID)
	}
	if r.results[output.ToolCallID] {
		return fmt.Errorf("duplicate result for %q", output.ToolCallID)
	}
	if tool.Name != output.Name {
		return fmt.Errorf("call/result name mismatch for %q", output.ToolCallID)
	}
	r.results[output.ToolCallID] = true
	tool.Result, tool.Error, tool.Bounds = output.Result, output.Error, output.Bounds
	if output.Refs.Result != "" {
		tool.Result, err = r.owner.content(output.Refs.Result)
		if err != nil {
			return err
		}
	}
	if output.Telemetry != nil {
		tool.Duration = time.Duration(output.Telemetry.DurationMs) * time.Millisecond
	}
	return nil
}

// readFinal selects only an assistant entry naming an adopted invocation, and
// restores its messages with the same precision-preserving decoder as responses.
func (r *runBuilder) readFinal(entry index.MessageEntry) error {
	if entry.Call == nil || !r.adopted[*entry.Call] {
		return nil
	}
	r.out.Final = nil
	if entry.Hash == "" {
		return nil
	}
	data, err := r.owner.content(entry.Hash)
	if err != nil {
		return err
	}
	wrapped := append([]byte(`{"Content":`), data...)
	wrapped = append(wrapped, '}')
	response, err := callrecord.DecodeResponse(wrapped)
	if err != nil {
		return err
	}
	for i := range response.Content {
		if response.Content[i].Role == model.ConversationRoleAssistant {
			r.out.Final = &response.Content[i]
		}
	}
	return nil
}
