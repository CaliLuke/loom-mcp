package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/toolerrors"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// runCompletedPayload is used to serialize RunCompletedEvent for transport.
	// It converts the error to a string since errors cannot be directly serialized.
	runCompletedPayload struct {
		Status         string    `json:"status"`
		Phase          run.Phase `json:"phase"`
		PublicError    string    `json:"public_error,omitempty"`
		Error          string    `json:"error,omitempty"`
		ErrorProvider  string    `json:"error_provider,omitempty"`
		ErrorOperation string    `json:"error_operation,omitempty"`
		ErrorKind      string    `json:"error_kind,omitempty"`
		ErrorCode      string    `json:"error_code,omitempty"`
		HTTPStatus     int       `json:"http_status,omitempty"`
		Retryable      bool      `json:"retryable"`
	}

	turnIDSetter interface {
		SetTurnID(string)
	}

	timestampSetter interface {
		SetTimestampMS(int64)
	}

	eventKeySetter interface {
		SetEventKey(string)
	}

	toolResultReceivedPayload struct {
		ToolCallID       string                   `json:"tool_call_id"`
		ParentToolCallID string                   `json:"parent_tool_call_id,omitempty"`
		ToolName         tools.Ident              `json:"tool_name"`
		Result           any                      `json:"result,omitempty"`
		ResultJSON       rawjson.Message          `json:"result_json,omitempty"`
		ServerData       rawjson.Message          `json:"server_data,omitempty"`
		ResultPreview    string                   `json:"result_preview,omitempty"`
		Bounds           *agent.Bounds            `json:"bounds,omitempty"`
		Duration         time.Duration            `json:"duration"`
		Telemetry        *telemetry.ToolTelemetry `json:"telemetry,omitempty"`
		RetryHint        *planner.RetryHint       `json:"retry_hint,omitempty"`
		Error            *toolerrors.ToolError    `json:"error,omitempty"`
	}
)

// EncodeToHookInput creates a hook activity input envelope from a hook event for
// serialization and transport to the hook activity.
func EncodeToHookInput(evt Event, turnID string) (*ActivityInput, error) {
	payload, err := encodeHookPayload(evt)
	if err != nil {
		return nil, err
	}

	return &ActivityInput{
		Type:        evt.Type(),
		EventKey:    evt.EventKey(),
		RunID:       evt.RunID(),
		AgentID:     agent.Ident(evt.AgentID()),
		SessionID:   evt.SessionID(),
		TurnID:      turnID,
		TimestampMS: evt.Timestamp(),
		Payload:     payload,
	}, nil
}

func encodeHookPayload(evt Event) (rawjson.Message, error) {
	switch e := evt.(type) {
	case *RunCompletedEvent:
		return encodeRunCompletedPayload(e)
	case *ToolResultReceivedEvent:
		return encodeToolResultPayload(e)
	default:
		return marshalHookPayload(string(evt.Type()), evt)
	}
}

func encodeRunCompletedPayload(e *RunCompletedEvent) (rawjson.Message, error) {
	p := runCompletedPayload{
		Status:         e.Status,
		Phase:          e.Phase,
		PublicError:    e.PublicError,
		ErrorProvider:  e.ErrorProvider,
		ErrorOperation: e.ErrorOperation,
		ErrorKind:      e.ErrorKind,
		ErrorCode:      e.ErrorCode,
		HTTPStatus:     e.HTTPStatus,
		Retryable:      e.Retryable,
	}
	if e.Error != nil {
		p.Error = e.Error.Error()
	}
	return marshalHookPayload("run completed", p)
}

func encodeToolResultPayload(e *ToolResultReceivedEvent) (rawjson.Message, error) {
	return marshalHookPayload("tool result", toolResultReceivedPayload{
		ToolCallID:       e.ToolCallID,
		ParentToolCallID: e.ParentToolCallID,
		ToolName:         e.ToolName,
		Result:           e.Result,
		ResultJSON:       e.ResultJSON,
		ServerData:       e.ServerData,
		ResultPreview:    e.ResultPreview,
		Bounds:           e.Bounds,
		Duration:         e.Duration,
		Telemetry:        e.Telemetry,
		RetryHint:        e.RetryHint,
		Error:            e.Error,
	})
}

func marshalHookPayload(label string, payload any) (rawjson.Message, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s payload: %w", label, err)
	}
	return rawjson.Message(b), nil
}

// DecodeFromHookInput reconstructs a hooks.Event from the serialized hook input.
func DecodeFromHookInput(input *ActivityInput) (Event, error) {
	evt, handled, err := decodeRunEvent(input)
	if err != nil {
		return nil, err
	}
	if !handled {
		evt, handled, err = decodeAwaitEvent(input)
	}
	if err != nil {
		return nil, err
	}
	if !handled {
		evt, handled, err = decodeToolEvent(input)
	}
	if err != nil {
		return nil, err
	}
	if !handled {
		evt, handled, err = decodeAuxEvent(input)
	}
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, fmt.Errorf("unsupported hook event type %q", input.Type)
	}
	if input.TurnID != "" {
		stampTurnID(evt, input.TurnID)
	}
	stampTimestamp(evt, input.TimestampMS)
	stampEventKey(evt, input.EventKey)
	return evt, nil
}

func decodeRunEvent(input *ActivityInput) (Event, bool, error) {
	//nolint:exhaustive // Event groups are intentionally partitioned across helper switches.
	switch input.Type {
	case RunStarted:
		var p RunStartedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewRunStartedEvent(input.RunID, input.AgentID, p.RunContext, p.Input), true, nil
	case RunPhaseChanged:
		var p RunPhaseChangedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewRunPhaseChangedEvent(input.RunID, input.AgentID, input.SessionID, p.Phase), true, nil
	case RunPaused:
		var p RunPausedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewRunPausedEvent(input.RunID, input.AgentID, input.SessionID, p.Reason, p.RequestedBy, p.Labels, p.Metadata), true, nil
	case RunResumed:
		var p RunResumedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewRunResumedEvent(input.RunID, input.AgentID, input.SessionID, p.Notes, p.RequestedBy, p.Labels, p.MessageCount), true, nil
	case RunCompleted:
		var p runCompletedPayload
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return newRunCompletedFromPayload(input, p), true, nil
	default:
		return nil, false, nil
	}
}

func decodeAwaitEvent(input *ActivityInput) (Event, bool, error) {
	//nolint:exhaustive // Event groups are intentionally partitioned across helper switches.
	switch input.Type {
	case PromptRendered:
		return nil, false, nil
	case AwaitClarification:
		return decodeAwaitClarificationEvent(input)
	case AwaitQuestions:
		return decodeAwaitQuestionsEvent(input)
	case AwaitConfirmation:
		return decodeAwaitConfirmationEvent(input)
	case AwaitExternalTools:
		return decodeAwaitExternalToolsEvent(input)
	case ToolAuthorization:
		return decodeToolAuthorizationEvent(input)
	default:
		return nil, false, nil
	}
}

func decodeToolEvent(input *ActivityInput) (Event, bool, error) {
	//nolint:exhaustive // Event groups are intentionally partitioned across helper switches.
	switch input.Type {
	case ChildRunLinked:
		return decodeChildRunLinkedEvent(input)
	case ToolCallArgsDelta:
		return decodeToolCallArgsDeltaEvent(input)
	case ToolCallScheduled:
		return decodeToolCallScheduledEvent(input)
	case ToolCallUpdated:
		return decodeToolCallUpdatedEvent(input)
	case ToolResultReceived:
		return decodeToolResultReceivedEvent(input)
	case RetryHintIssued:
		return decodeRetryHintIssuedEvent(input)
	default:
		return nil, false, nil
	}
}

func decodeAuxEvent(input *ActivityInput) (Event, bool, error) {
	//nolint:exhaustive // Event groups are intentionally partitioned across helper switches.
	switch input.Type {
	case PromptRendered:
		return decodePromptRenderedEvent(input)
	case AssistantMessage:
		return decodeAssistantMessageEvent(input)
	case PlannerNote:
		return decodePlannerNoteEvent(input)
	case ThinkingBlock:
		return decodeThinkingBlockEvent(input)
	case PolicyDecision:
		return decodePolicyDecisionEvent(input)
	case MemoryAppended:
		return decodeMemoryAppendedEvent(input)
	case Usage:
		return decodeUsageEvent(input)
	case HardProtectionTriggered:
		return decodeHardProtectionEvent(input)
	default:
		return nil, false, nil
	}
}

func newRunCompletedFromPayload(input *ActivityInput, p runCompletedPayload) Event {
	var runErr error
	if p.Error != "" {
		runErr = errors.New(p.Error)
	}
	rc := NewRunCompletedEvent(input.RunID, input.AgentID, input.SessionID, p.Status, p.Phase, runErr)
	rc.PublicError = p.PublicError
	rc.ErrorProvider = p.ErrorProvider
	rc.ErrorOperation = p.ErrorOperation
	rc.ErrorKind = p.ErrorKind
	rc.ErrorCode = p.ErrorCode
	rc.HTTPStatus = p.HTTPStatus
	rc.Retryable = p.Retryable
	return rc
}

func decodeHookPayload(input *ActivityInput, payload any) error {
	if err := json.Unmarshal(input.Payload, payload); err != nil {
		return fmt.Errorf("decode %s payload: %w", input.Type, err)
	}
	return nil
}

func decodeAwaitClarificationEvent(input *ActivityInput) (Event, bool, error) {
	var p AwaitClarificationEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewAwaitClarificationEvent(input.RunID, input.AgentID, input.SessionID, p.ID, p.Question, p.MissingFields, p.RestrictToTool, p.ExampleInput), true, nil
}

func decodeAwaitQuestionsEvent(input *ActivityInput) (Event, bool, error) {
	var p AwaitQuestionsEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewAwaitQuestionsEvent(input.RunID, input.AgentID, input.SessionID, p.ID, p.ToolName, p.ToolCallID, p.Payload, p.Title, p.Questions), true, nil
}

func decodeAwaitConfirmationEvent(input *ActivityInput) (Event, bool, error) {
	var p AwaitConfirmationEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewAwaitConfirmationEvent(input.RunID, input.AgentID, input.SessionID, p.ID, p.Title, p.Prompt, p.ToolName, p.ToolCallID, p.Payload), true, nil
}

func decodeAwaitExternalToolsEvent(input *ActivityInput) (Event, bool, error) {
	var p AwaitExternalToolsEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewAwaitExternalToolsEvent(input.RunID, input.AgentID, input.SessionID, p.ID, p.Items), true, nil
}

func decodeToolAuthorizationEvent(input *ActivityInput) (Event, bool, error) {
	var p ToolAuthorizationEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewToolAuthorizationEvent(input.RunID, input.AgentID, input.SessionID, p.ToolName, p.ToolCallID, p.Approved, p.Summary, p.ApprovedBy), true, nil
}

func decodeChildRunLinkedEvent(input *ActivityInput) (Event, bool, error) {
	var p ChildRunLinkedEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewChildRunLinkedEvent(input.RunID, input.AgentID, input.SessionID, p.ToolName, p.ToolCallID, p.ChildRunID, p.ChildAgentID), true, nil
}

func decodeToolCallArgsDeltaEvent(input *ActivityInput) (Event, bool, error) {
	var p ToolCallArgsDeltaEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewToolCallArgsDeltaEvent(input.RunID, input.AgentID, input.SessionID, p.ToolCallID, p.ToolName, p.Delta), true, nil
}

func decodeToolCallScheduledEvent(input *ActivityInput) (Event, bool, error) {
	var p ToolCallScheduledEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewToolCallScheduledEvent(input.RunID, input.AgentID, input.SessionID, p.ToolName, p.ToolCallID, p.Payload, p.Queue, p.ParentToolCallID, p.ExpectedChildrenTotal), true, nil
}

func decodeToolCallUpdatedEvent(input *ActivityInput) (Event, bool, error) {
	var p ToolCallUpdatedEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewToolCallUpdatedEvent(input.RunID, input.AgentID, input.SessionID, p.ToolCallID, p.ExpectedChildrenTotal), true, nil
}

func decodeToolResultReceivedEvent(input *ActivityInput) (Event, bool, error) {
	var p toolResultReceivedPayload
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewToolResultReceivedEvent(input.RunID, input.AgentID, input.SessionID, p.ToolName, p.ToolCallID, p.ParentToolCallID, p.Result, p.ResultJSON, p.ServerData, p.ResultPreview, p.Bounds, p.Duration, p.Telemetry, p.RetryHint, p.Error), true, nil
}

func decodeRetryHintIssuedEvent(input *ActivityInput) (Event, bool, error) {
	var p RetryHintIssuedEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewRetryHintIssuedEvent(input.RunID, input.AgentID, input.SessionID, p.Reason, p.ToolName, p.Message), true, nil
}

func decodePromptRenderedEvent(input *ActivityInput) (Event, bool, error) {
	var p PromptRenderedEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewPromptRenderedEvent(input.RunID, input.AgentID, input.SessionID, p.PromptID, p.Version, p.Scope), true, nil
}

func decodeAssistantMessageEvent(input *ActivityInput) (Event, bool, error) {
	var p AssistantMessageEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewAssistantMessageEvent(input.RunID, input.AgentID, input.SessionID, p.Message, p.Structured), true, nil
}

func decodePlannerNoteEvent(input *ActivityInput) (Event, bool, error) {
	var p PlannerNoteEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewPlannerNoteEvent(input.RunID, input.AgentID, input.SessionID, p.Note, p.Labels), true, nil
}

func decodeThinkingBlockEvent(input *ActivityInput) (Event, bool, error) {
	var p ThinkingBlockEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewThinkingBlockEvent(input.RunID, input.AgentID, input.SessionID, p.Text, p.Signature, p.Redacted, p.ContentIndex, p.Final), true, nil
}

func decodePolicyDecisionEvent(input *ActivityInput) (Event, bool, error) {
	var p PolicyDecisionEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewPolicyDecisionEvent(input.RunID, input.AgentID, input.SessionID, p.AllowedTools, p.Caps, p.Labels, p.Metadata), true, nil
}

func decodeMemoryAppendedEvent(input *ActivityInput) (Event, bool, error) {
	var p MemoryAppendedEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewMemoryAppendedEvent(input.RunID, input.AgentID, input.SessionID, p.EventCount), true, nil
}

func decodeUsageEvent(input *ActivityInput) (Event, bool, error) {
	var p UsageEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewUsageEvent(input.RunID, input.AgentID, input.SessionID, p.TokenUsage), true, nil
}

func decodeHardProtectionEvent(input *ActivityInput) (Event, bool, error) {
	var p HardProtectionEvent
	if err := decodeHookPayload(input, &p); err != nil {
		return nil, false, err
	}
	return NewHardProtectionEvent(input.RunID, input.AgentID, input.SessionID, p.Reason, p.ExecutedAgentTools, p.ChildrenTotal, p.ToolNames), true, nil
}

func stampTurnID(evt Event, turnID string) {
	evt.(turnIDSetter).SetTurnID(turnID)
}

func stampTimestamp(evt Event, timestampMS int64) {
	evt.(timestampSetter).SetTimestampMS(timestampMS)
}

func stampEventKey(evt Event, eventKey string) {
	evt.(eventKeySetter).SetEventKey(eventKey)
}
