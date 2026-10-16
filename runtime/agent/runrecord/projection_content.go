package runrecord

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
)

// projectionPartValue accepts the same pointer/value forms as the model codec.
func projectionPartValue(part model.Part) model.Part {
	switch p := part.(type) {
	case *model.TextPart:
		return projectionDereference(p)
	case *model.ThinkingPart:
		return projectionDereference(p)
	case *model.CitationsPart:
		return projectionDereference(p)
	case *model.ImagePart:
		return projectionDereference(p)
	case *model.DocumentPart:
		return projectionDereference(p)
	case *model.ToolUsePart:
		return projectionDereference(p)
	case *model.ToolResultPart:
		return projectionDereference(p)
	case *model.CacheCheckpointPart:
		return projectionDereference(p)
	default:
		return part
	}
}

func projectionDereference[T model.Part](part *T) model.Part {
	if part == nil {
		return nil
	}
	return *part
}

// projectRun constructs only sharing fields; raw envelopes are never traversed
// as generic JSON, so duplicate opaque request/event/content paths cannot leak.
func (b *projectionBuilder) projectRun(record *RunRecord, source *Run, path string) ProjectedRun {
	out := ProjectedRun{RunID: source.RunID, Sealed: source.Sealed}
	out.Transcript = b.messages(source.Transcript, path+"/transcript")
	for i := range source.Calls {
		out.Calls = append(out.Calls, b.call(record, &source.Calls[i], fmt.Sprintf("%s/calls/%d", path, i)))
	}
	for i, event := range source.Events {
		eventPath := fmt.Sprintf("%s/events/%d", path, i)
		if event == nil {
			b.fail(eventPath, "invalid_event", fmt.Errorf("nil event"))
			continue
		}
		projected, ok := b.event(event, eventPath)
		if ok {
			out.Events = append(out.Events, projected)
		}
	}
	groups := []struct {
		name, kind string
		values     []ContentPart
	}{
		{"typed_answers", "typed_answer", source.TypedAnswers}, {"await_answers", "await_answer", source.AwaitAnswers},
		{"results", "tool_result", source.Results}, {"server_only", "server_only", source.ServerOnly},
	}
	for _, group := range groups {
		for i, part := range group.values {
			valuePath := fmt.Sprintf("%s/%s/%d", path, group.name, i)
			// Stored payloads are application-owned JSON, not model-message
			// envelopes. Their policy classification is explicit at this boundary.
			value := b.value(group.kind, valuePath, jsontext.Value(part.Content))
			if value != nil {
				out.Content = append(out.Content, ProjectedValue{Kind: group.kind, Path: valuePath, Value: value})
			}
		}
	}
	// These structures carry private configuration and duplicated content. The
	// sharing schema deliberately excludes them and reports that boundary.
	for _, field := range []string{"entries", "checkpoints", "presentations", "index_head", "context_settings", "context_fingerprint", "recovery_settings", "compaction_settings", "summary", "recall", "keep_result_tools", "tool_result_parts", "nontext_resident", "workflow_protocol_version", "pos", "prev"} {
		b.omit(path+"/"+field, "raw_evidence")
	}
	return out
}

func (b *projectionBuilder) messages(messages []*model.Message, path string) []ProjectedMessage {
	var out []ProjectedMessage
	for i, message := range messages {
		messagePath := fmt.Sprintf("%s/%d", path, i)
		if message == nil {
			b.fail(messagePath, "invalid_message", fmt.Errorf("nil message"))
			continue
		}
		b.role = message.Role
		projected := ProjectedMessage{Role: message.Role}
		if len(message.Meta) != 0 {
			projected.Metadata = b.value("metadata", messagePath+"/metadata", message.Meta)
		}
		for j, part := range message.Parts {
			value, ok := b.part(part, fmt.Sprintf("%s/parts/%d", messagePath, j))
			if ok {
				projected.Parts = append(projected.Parts, value)
			}
		}
		out = append(out, projected)
		b.role = ""
	}
	return out
}

func (b *projectionBuilder) part(part model.Part, path string) (ProjectedValue, bool) {
	out, content, ok := b.classifyPart(projectionPartValue(part), path)
	if !ok {
		return out, false
	}
	out.Value = b.value(out.Kind, path, content)
	// Preserve tool attribution even when its content is omitted.
	return out, out.Value != nil || out.ToolID != "" || len(out.Attachments) != 0
}

func (b *projectionBuilder) classifyPart(part model.Part, path string) (ProjectedValue, any, bool) {
	out := ProjectedValue{Path: path}
	var content any
	switch p := part.(type) {
	case model.TextPart:
		out.Kind = "message"
		content = p.Text
	case model.ThinkingPart:
		out.Kind = "reasoning"
		content = p
	case model.CitationsPart:
		out.Kind = "message"
		content = p.Text
		b.omit(path+"/citations", "attachment")
	case model.ImagePart:
		out.Kind = "attachment"
		content = p
	case model.DocumentPart:
		out.Kind = "attachment"
		content = p
	case model.ToolUsePart:
		out.Kind = "tool_arguments"
		out.ToolID = p.ID
		out.ToolName = p.Name
		content = p.Input
	case model.ToolResultPart:
		out.Kind = "tool_result"
		out.ToolID = p.ToolUseID
		out.IsError = p.IsError
		content = p.Content
		out.Attachments = b.attachments(p.Parts, path)
	case model.CacheCheckpointPart:
		b.omit(path, "cache_checkpoint")
		return out, nil, false
	default:
		b.fail(path, "unsupported_part", fmt.Errorf("unsupported part %T", part))
		return out, nil, false
	}
	return out, content, true
}

func (b *projectionBuilder) call(record *RunRecord, source *Call, path string) ProjectedCall {
	out := ProjectedCall{Key: source.Record.Key, Class: source.Class, State: source.Record.State, Activity: string(source.Activity), Step: source.Step, Model: source.Started.Model, Started: source.Started.Started}
	b.omit(path+"/record", "raw_evidence")
	b.omit(path+"/started/request", "raw_evidence")
	b.omit(path+"/started/prompts", "metadata")
	if source.Request != nil {
		b.omit(path+"/request/configuration", "raw_evidence")
		out.Request = b.messages(source.Request.Messages, path+"/request/messages")
	}
	if source.Completion == nil {
		return out
	}
	completion := source.Completion
	out.Usage = completion.Usage
	out.LatencyNS = int64(completion.Latency)
	if completion.Error != nil {
		out.ErrorKind = completion.Error.Kind
		out.Error = b.value("diagnostic", path+"/error", completion.Error.Message)
	}
	b.response(record, completion, path, &out)
	return out
}

func (b *projectionBuilder) response(record *RunRecord, completion *callrecord.Completion, path string, out *ProjectedCall) {
	if completion.Response == nil {
		return
	}
	responsePath := path + "/response"
	bytes, ok := record.Content[*completion.Response]
	if !ok {
		b.fail(responsePath, "missing_response", fmt.Errorf("missing recorded response"))
		return
	}
	response, err := callrecord.DecodeResponse(bytes)
	if err != nil {
		b.fail(responsePath, "invalid_response", err)
		return
	}
	if response == nil {
		return
	}
	messages := make([]*model.Message, len(response.Content))
	for i := range response.Content {
		messages[i] = &response.Content[i]
	}
	out.Response = b.messages(messages, responsePath+"/messages")
	for i, tool := range response.ToolCalls {
		toolPath := fmt.Sprintf("%s/tool_calls/%d", responsePath, i)
		value := b.value("tool_arguments", toolPath, jsontext.Value(tool.Payload))
		out.ToolCalls = append(out.ToolCalls, ProjectedValue{Kind: "tool_arguments", Path: toolPath, ToolID: tool.ID, ToolName: string(tool.Name), Value: value})
	}
}

func (b *projectionBuilder) event(source *runlog.Event, path string) (ProjectedEvent, bool) {
	out := ProjectedEvent{ID: source.ID, Type: string(source.Type), AgentID: string(source.AgentID), SessionID: source.SessionID, TurnID: source.TurnID, Timestamp: source.Timestamp}
	switch string(source.Type) {
	case string(hooks.ChildRunLinked):
		var payload struct{ ChildRunID, ChildAgentID, ToolCallID, ToolName string }
		if err := json.Unmarshal(source.Payload, &payload); err != nil {
			b.fail(path, "invalid_child_link", err)
			return out, false
		}
		out.ChildRunID = payload.ChildRunID
		out.ChildAgentID = payload.ChildAgentID
		out.ToolID = payload.ToolCallID
		out.ToolName = payload.ToolName
	case string(hooks.RunCompleted):
		var payload struct {
			Status      string `json:"status"`
			Phase       string `json:"phase"`
			Error       string `json:"error"`
			PublicError string `json:"public_error"`
		}
		if err := json.Unmarshal(source.Payload, &payload); err != nil {
			b.fail(path, "invalid_terminal_event", err)
			return out, false
		}
		out.Status = payload.Status
		out.Phase = payload.Phase
		if payload.Error != "" || payload.PublicError != "" {
			out.Diagnostic = b.value("diagnostic", path+"/diagnostic", map[string]string{"error": payload.Error, "public_error": payload.PublicError})
		}
	default:
		b.omit(path+"/payload", "event_payload")
		return out, false
	}
	return out, true
}

// value isolates both directions of the policy boundary and distinguishes an
// intentional omission from a failed or invalid transformation.
func (b *projectionBuilder) value(kind, path string, source any) jsontext.Value {
	data, err := callrecord.Encode(source)
	if err != nil {
		b.fail(path, "encode_content", err)
		return nil
	}
	value, include, err := b.policy.Content(ProjectionContent{Kind: kind, Path: path, RunID: b.runID, Role: b.role, Value: append(jsontext.Value(nil), data...)})
	if err != nil {
		b.fail(path, "policy_error", err)
		return nil
	}
	if !include {
		b.omit(path, kind)
		return nil
	}
	if !value.IsValid() {
		b.fail(path, "invalid_policy_json", fmt.Errorf("policy returned invalid JSON"))
		return nil
	}
	return append(jsontext.Value(nil), value...)
}

func (b *projectionBuilder) omit(path, kind string) {
	b.artifact.Omissions = append(b.artifact.Omissions, ProjectionOmission{Path: path, Kind: kind})
}

func (b *projectionBuilder) fail(path, code string, err error) {
	b.artifact.Failures = append(b.artifact.Failures, ProjectionFailure{Path: path, Code: code})
	b.errors = append(b.errors, fmt.Errorf("projection %s %s: %w", path, code, err))
}

func (b *projectionBuilder) attachments(parts []model.ToolResultContentPart, path string) []ProjectedValue {
	var out []ProjectedValue
	for i, attachment := range parts {
		value, ok := b.part(attachment, fmt.Sprintf("%s/attachments/%d", path, i))
		if ok {
			out = append(out, value)
		}
	}
	return out
}
