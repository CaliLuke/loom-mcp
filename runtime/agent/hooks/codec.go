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
	var payload rawjson.Message
	switch e := evt.(type) {
	case *RunCompletedEvent:
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
		b, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshal run completed payload: %w", err)
		}
		payload = rawjson.Message(b)
	case *ToolResultReceivedEvent:
		p := toolResultReceivedPayload{
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
		}
		b, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshal tool result payload: %w", err)
		}
		payload = rawjson.Message(b)
	default:
		b, err := json.Marshal(evt)
		if err != nil {
			return nil, fmt.Errorf("marshal hook event payload %q: %w", evt.Type(), err)
		}
		payload = rawjson.Message(b)
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
		var p AwaitClarificationEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewAwaitClarificationEvent(input.RunID, input.AgentID, input.SessionID, p.ID, p.Question, p.MissingFields, p.RestrictToTool, p.ExampleInput), true, nil
	case AwaitQuestions:
		var p AwaitQuestionsEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewAwaitQuestionsEvent(
			input.RunID,
			input.AgentID,
			input.SessionID,
			p.ID,
			p.ToolName,
			p.ToolCallID,
			p.Payload,
			p.Title,
			p.Questions,
		), true, nil
	case AwaitConfirmation:
		var p AwaitConfirmationEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewAwaitConfirmationEvent(input.RunID, input.AgentID, input.SessionID, p.ID, p.Title, p.Prompt, p.ToolName, p.ToolCallID, p.Payload), true, nil
	case AwaitExternalTools:
		var p AwaitExternalToolsEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewAwaitExternalToolsEvent(input.RunID, input.AgentID, input.SessionID, p.ID, p.Items), true, nil
	case ToolAuthorization:
		var p ToolAuthorizationEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewToolAuthorizationEvent(input.RunID, input.AgentID, input.SessionID, p.ToolName, p.ToolCallID, p.Approved, p.Summary, p.ApprovedBy), true, nil
	default:
		return nil, false, nil
	}
}

func decodeToolEvent(input *ActivityInput) (Event, bool, error) {
	//nolint:exhaustive // Event groups are intentionally partitioned across helper switches.
	switch input.Type {
	case ChildRunLinked:
		var p ChildRunLinkedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewChildRunLinkedEvent(input.RunID, input.AgentID, input.SessionID, p.ToolName, p.ToolCallID, p.ChildRunID, p.ChildAgentID), true, nil
	case ToolCallArgsDelta:
		var p ToolCallArgsDeltaEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewToolCallArgsDeltaEvent(input.RunID, input.AgentID, input.SessionID, p.ToolCallID, p.ToolName, p.Delta), true, nil
	case ToolCallScheduled:
		var p ToolCallScheduledEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewToolCallScheduledEvent(input.RunID, input.AgentID, input.SessionID, p.ToolName, p.ToolCallID, p.Payload, p.Queue, p.ParentToolCallID, p.ExpectedChildrenTotal), true, nil
	case ToolCallUpdated:
		var p ToolCallUpdatedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewToolCallUpdatedEvent(input.RunID, input.AgentID, input.SessionID, p.ToolCallID, p.ExpectedChildrenTotal), true, nil
	case ToolResultReceived:
		var p toolResultReceivedPayload
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewToolResultReceivedEvent(
			input.RunID,
			input.AgentID,
			input.SessionID,
			p.ToolName,
			p.ToolCallID,
			p.ParentToolCallID,
			p.Result,
			p.ResultJSON,
			p.ServerData,
			p.ResultPreview,
			p.Bounds,
			p.Duration,
			p.Telemetry,
			p.RetryHint,
			p.Error,
		), true, nil
	case RetryHintIssued:
		var p RetryHintIssuedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewRetryHintIssuedEvent(input.RunID, input.AgentID, input.SessionID, p.Reason, p.ToolName, p.Message), true, nil
	default:
		return nil, false, nil
	}
}

func decodeAuxEvent(input *ActivityInput) (Event, bool, error) {
	//nolint:exhaustive // Event groups are intentionally partitioned across helper switches.
	switch input.Type {
	case PromptRendered:
		var p PromptRenderedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewPromptRenderedEvent(input.RunID, input.AgentID, input.SessionID, p.PromptID, p.Version, p.Scope), true, nil
	case AssistantMessage:
		var p AssistantMessageEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewAssistantMessageEvent(input.RunID, input.AgentID, input.SessionID, p.Message, p.Structured), true, nil
	case PlannerNote:
		var p PlannerNoteEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewPlannerNoteEvent(input.RunID, input.AgentID, input.SessionID, p.Note, p.Labels), true, nil
	case ThinkingBlock:
		var p ThinkingBlockEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewThinkingBlockEvent(
			input.RunID,
			input.AgentID,
			input.SessionID,
			p.Text,
			p.Signature,
			p.Redacted,
			p.ContentIndex,
			p.Final,
		), true, nil
	case PolicyDecision:
		var p PolicyDecisionEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewPolicyDecisionEvent(input.RunID, input.AgentID, input.SessionID, p.AllowedTools, p.Caps, p.Labels, p.Metadata), true, nil
	case MemoryAppended:
		var p MemoryAppendedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewMemoryAppendedEvent(input.RunID, input.AgentID, input.SessionID, p.EventCount), true, nil
	case Usage:
		var p UsageEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewUsageEvent(input.RunID, input.AgentID, input.SessionID, p.TokenUsage), true, nil
	case HardProtectionTriggered:
		var p HardProtectionEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		return NewHardProtectionEvent(input.RunID, input.AgentID, input.SessionID, p.Reason, p.ExecutedAgentTools, p.ChildrenTotal, p.ToolNames), true, nil
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

func stampTurnID(evt Event, turnID string) {
	evt.(turnIDSetter).SetTurnID(turnID)
}

func stampTimestamp(evt Event, timestampMS int64) {
	evt.(timestampSetter).SetTimestampMS(timestampMS)
}

func stampEventKey(evt Event, eventKey string) {
	evt.(eventKeySetter).SetEventKey(eventKey)
}
