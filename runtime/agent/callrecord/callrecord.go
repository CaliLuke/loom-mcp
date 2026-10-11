// Package callrecord encodes model call evidence and rebuilds exact requests.
// Stores retain opaque bodies; this package owns their deterministic JSON v2 format.
package callrecord

import (
	jsonopts "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// ActivityKind identifies the activity that owns a model call.
	ActivityKind string

	// Started is the immutable request and identity written before sending.
	// Step is the planner step; TurnID remains the conversational turn.
	Started struct {
		Key      runstore.CallKey   `json:"key"`
		Activity ActivityKind       `json:"activity"`
		TurnID   string             `json:"turn_id"`
		Step     int                `json:"step"`
		Request  RequestParts       `json:"request"`
		Started  time.Time          `json:"started"`
		Model    string             `json:"model"`
		ModelID  string             `json:"model_id"`
		Class    model.ModelClass   `json:"class"`
		Context  string             `json:"context"`
		Prompts  []prompt.PromptRef `json:"prompts"`
	}

	// Completion is the terminal outcome, written after storing any response.
	// Facts is reserved for K10 and remains nil.
	Completion struct {
		Response *runstore.Hash   `json:"response"`
		Error    *CallError       `json:"error"`
		Usage    model.TokenUsage `json:"usage"`
		Latency  time.Duration    `json:"latency"`
		Facts    *jsontext.Value  `json:"facts"`
	}

	// CallError preserves the failure classification and diagnostic message.
	CallError struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}

	// MessageSegment is either a zero-based half-open view span or one message hash.
	// A nonempty Hash requires both positions to be zero.
	MessageSegment struct {
		ViewFrom int           `json:"view_from,omitzero"`
		ViewTo   int           `json:"view_to,omitzero"`
		Hash     runstore.Hash `json:"hash,omitzero"`
	}

	// RequestParts names the request content and its ordered message segments.
	// View is the committed reference used by spans, or nil when no spans exist.
	RequestParts struct {
		System   runstore.Hash    `json:"system"`
		Tools    runstore.Hash    `json:"tools"`
		View     *index.ViewRef   `json:"view"`
		Messages []MessageSegment `json:"messages"`
		Params   runstore.Hash    `json:"params"`
		// Request is the canonical verification digest, not stored content.
		Request runstore.Hash `json:"request"`
	}

	// Params contains every request field outside messages, tools and provenance.
	Params struct {
		RunID            string                  `json:"run_id"`
		Temperature      float32                 `json:"temperature"`
		ToolChoice       *model.ToolChoice       `json:"tool_choice"`
		MaxTokens        int                     `json:"max_tokens"`
		StructuredOutput *model.StructuredOutput `json:"structured_output"`
		Stream           bool                    `json:"stream"`
		Thinking         *model.ThinkingOptions  `json:"thinking"`
		Cache            *model.CacheOptions     `json:"cache"`
	}

	// MessageBytes measures encoded request messages inside and outside the view.
	// Counts include repetitions; a hash segment contributes outside bytes.
	MessageBytes struct {
		Inside  int `json:"inside"`
		Outside int `json:"outside"`
	}
)

const (
	// ActivityPlanner identifies planner calls, including auxiliary calls.
	ActivityPlanner ActivityKind = "planner"
	// ActivitySummarize reserves the summarizer activity kind for K15.
	ActivitySummarize ActivityKind = "summarize"
)

var (
	// ErrRequestMismatch means the rebuilt request differs from the recorded request.
	ErrRequestMismatch = errors.New("callrecord request rebuild mismatch")
)

// Encode returns deterministic JSON v2, preserving numeric tokens and durations.
// Reordering raw objects also sorts maps emitted by Message.MarshalJSON.
func Encode(value any) ([]byte, error) {
	data, err := json.Marshal(value, json.Deterministic(true), jsonopts.FormatDurationAsNano(true))
	if err != nil {
		return nil, fmt.Errorf("encode callrecord: %w", err)
	}
	raw := jsontext.Value(data)
	if err := raw.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return nil, fmt.Errorf("order callrecord JSON: %w", err)
	}
	return raw, nil
}

// DecodeStarted restores a started body using the codec's duration options.
func DecodeStarted(data []byte) (Started, error) {
	var started Started
	if err := json.Unmarshal(data, &started, jsonopts.FormatDurationAsNano(true)); err != nil {
		return Started{}, fmt.Errorf("decode callrecord started: %w", err)
	}
	return started, nil
}

// DecodeCompletion restores a completion body, including nanosecond latency.
func DecodeCompletion(data []byte) (Completion, error) {
	var completion Completion
	if err := json.Unmarshal(data, &completion, jsonopts.FormatDurationAsNano(true)); err != nil {
		return Completion{}, fmt.Errorf("decode callrecord completion: %w", err)
	}
	return completion, nil
}
