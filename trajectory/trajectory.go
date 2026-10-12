// Package trajectory derives causal evaluation steps from self-contained run records.
package trajectory

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Options supplies a caller's final output and observer-event trust boundary.
	Options struct {
		// Output, when present, supplies Final even if that field is nil.
		// It applies only to the requested run, not its linked children.
		Output *api.RunOutput `json:"output,omitzero"`
		// EventsUnverified marks observed steps and status as unverified.
		// Record-derived model, tool and answer steps remain verified.
		EventsUnverified bool `json:"events_unverified"`
	}

	// Trajectory contains one sealed run's causal steps and run-local totals.
	Trajectory struct {
		// Pos and Prev preserve optional session continuation identity.
		Pos  int     `json:"pos,omitzero"`
		Prev *string `json:"prev,omitzero"`

		// TraceID optionally correlates the run with caller-owned distributed tracing.
		TraceID        string         `json:"trace_id,omitempty"`
		RunID          string         `json:"run_id"`
		SessionID      string         `json:"session_id"`
		AgentID        agent.Ident    `json:"agent_id"`
		Steps          []Step         `json:"steps"`
		Final          *model.Message `json:"final"`
		Status         string         `json:"status"`
		StatusVerified bool           `json:"status_verified"`
		Totals         Totals         `json:"totals"`
	}

	// Session preserves trajectories in the caller's run order.
	Session []*Trajectory

	// StepKind identifies the populated member of a Step.
	StepKind string

	// Step is one model call, tool call, stored answer or observed event.
	// Step is the planner schedule number; EntryID is zero for non-entry steps.
	// Exactly one of Model, Tool, Event and Answer is populated.
	Step struct {
		Kind     StepKind        `json:"kind"`
		Step     int             `json:"step"`
		EntryID  index.MessageID `json:"entry_id,omitzero"`
		Verified bool            `json:"verified"`
		Model    *ModelStep      `json:"model,omitzero"`
		Tool     *ToolStep       `json:"tool,omitzero"`
		Event    *ObservedEvent  `json:"event,omitzero"`
		Answer   *AnswerStep     `json:"answer,omitzero"`
	}

	// ModelStep preserves one recorded invocation, including abandoned and open calls.
	ModelStep struct {
		Key           runstore.CallKey             `json:"key"`
		Class         runrecord.CallClass          `json:"class"`
		Activity      callrecord.ActivityKind      `json:"activity"`
		Step          int                          `json:"step"`
		Model         string                       `json:"model"`
		ModelID       string                       `json:"model_id"`
		Usage         model.TokenUsage             `json:"usage"`
		Latency       time.Duration                `json:"latency"`
		Error         *callrecord.CallError        `json:"error,omitzero"`
		Facts         *contextmeasure.RequestFacts `json:"facts,omitzero"`
		Chosen        []model.ToolCall             `json:"chosen"`
		SystemHash    runstore.Hash                `json:"system_hash"`
		ToolsHash     runstore.Hash                `json:"tools_hash"`
		RequestDigest runstore.Hash                `json:"request_digest"`
		Prompts       []prompt.PromptRef           `json:"prompts"`
	}

	// ToolStep joins an original stored call to its full result and linked children.
	// Arguments are the planner's stored arguments before server injection.
	// Result is absent when no result has been recorded. Children follow their
	// owning call in canonical child-link order, regardless of completion order.
	ToolStep struct {
		Name             tools.Ident        `json:"name"`
		ToolCallID       string             `json:"tool_call_id"`
		ParentToolCallID string             `json:"parent_tool_call_id,omitzero"`
		Arguments        rawjson.Message    `json:"arguments"`
		Result           rawjson.Message    `json:"result"`
		Bounds           *agent.Bounds      `json:"bounds,omitzero"`
		Error            *planner.ToolError `json:"error,omitzero"`
		Duration         time.Duration      `json:"duration"`
		Children         []*Trajectory      `json:"children"`
	}

	// ObservedEvent preserves a selected run-log payload without numeric coercion.
	// Its containing Step owns the verification flag.
	ObservedEvent struct {
		Type      hooks.EventType `json:"type"`
		EventKey  string          `json:"event_key"`
		Timestamp time.Time       `json:"timestamp"`
		Payload   rawjson.Message `json:"payload"`
	}

	// AnswerStep preserves a complete clarification or typed-input answer.
	// Content is a JSON string for clarification text or the exact stored JSON
	// payload for typed input. It is encoded as a readable value, never base64.
	// Invalid UTF-8 in clarification text is replaced with U+FFFD.
	AnswerStep struct {
		Kind    index.EntryKind `json:"kind"`
		InputID string          `json:"input_id,omitzero"`
		Content json.RawMessage `json:"content"`
	}

	// Totals counts only this run's recorded calls; children have their own totals.
	// RepeatedToolCalls counts calls after the first with identical name and
	// key-sorted arguments. WallTime spans first to last record-event timestamp.
	Totals struct {
		TokensByModel     map[string]model.TokenUsage `json:"tokens_by_model"`
		ModelCallsByClass map[runrecord.CallClass]int `json:"model_calls_by_class"`
		ModelCalls        int                         `json:"model_calls"`
		AdoptedModelCalls int                         `json:"adopted_model_calls"`
		ToolCalls         int                         `json:"tool_calls"`
		FailedToolCalls   int                         `json:"failed_tool_calls"`
		RepeatedToolCalls int                         `json:"repeated_tool_calls"`
		WallTime          time.Duration               `json:"wall_time"`
	}
)

const (
	// StepModel identifies a recorded model invocation.
	StepModel StepKind = "model"
	// StepTool identifies a stored tool invocation and its result.
	StepTool StepKind = "tool"
	// StepAwait identifies an observed await prompt.
	StepAwait StepKind = "await"
	// StepAnswer identifies a stored answer or observed tool authorization.
	StepAnswer StepKind = "answer"
	// StepEvent identifies another selected observer event.
	StepEvent StepKind = "event"
)

var (
	// ErrNotTerminal means the requested run or one of its linked children is unsealed.
	ErrNotTerminal = errors.New("trajectory: run is not terminal")
)

// Marshal returns deterministic JSON with nanosecond durations and sorted objects.
func (t *Trajectory) Marshal() ([]byte, error) {
	return callrecord.Encode(t)
}

// Marshal returns deterministic JSON without changing the caller's run order.
func (s Session) Marshal() ([]byte, error) {
	return callrecord.Encode(s)
}
