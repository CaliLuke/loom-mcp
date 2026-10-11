package agenttest

import (
	"errors"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Turn describes one scripted response or failure. Construct it with Respond or Fail.
type Turn struct {
	parts         []Part
	matches       []Match
	failure       error
	fragments     int
	streamFailure error
	streamAfter   int
}

// Part configures a response's content or metadata.
type Part struct {
	apply func(*scriptResponse) error
}

type scriptResponse struct {
	thinking []string
	text     []string
	calls    []model.ToolCall
	usage    model.TokenUsage
	stop     string
	limited  bool
}

// Respond constructs a successful provider turn. Client validation still applies.
func Respond(parts ...Part) Turn {
	return Turn{parts: slices.Clone(parts), fragments: 1}
}

// Text adds an assistant text part.
func Text(text string) Part {
	return Part{apply: func(r *scriptResponse) error {
		r.text = append(r.text, text)
		return nil
	}}
}

// Thinking adds an assistant reasoning part, emitted before text.
func Thinking(text string) Part {
	return Part{apply: func(r *scriptResponse) error {
		r.thinking = append(r.thinking, text)
		return nil
	}}
}

// RawToolCall adds a tool call with raw JSON arguments. The validated client
// checks the identity and arguments against the actual request's tool schema.
func RawToolCall(name tools.Ident, payload rawjson.Message) Part {
	owned := slices.Clone(payload)
	return Part{apply: func(r *scriptResponse) error {
		r.calls = append(r.calls, model.ToolCall{Name: name, Payload: slices.Clone(owned)})
		return nil
	}}
}

// ToolCall adds a typed tool call encoded through the descriptor's payload
// codec. Encoding failures are returned by NewScript as builder errors.
func ToolCall[P, R any](tool tools.TypedTool[P, R], payload P) Part {
	return Part{apply: func(r *scriptResponse) error {
		if tool.Payload.ToJSON == nil {
			return fmt.Errorf("tool %q payload encoder is required", tool.Ident())
		}
		encoded, err := tool.Payload.ToJSON(payload)
		if err != nil {
			return fmt.Errorf("encode tool %q payload: %w", tool.Ident(), err)
		}
		r.calls = append(r.calls, model.ToolCall{Name: tool.Ident(), Payload: rawjson.Message(slices.Clone(encoded))})
		return nil
	}}
}

// CallID sets the ID of the immediately preceding tool call.
func CallID(id string) Part {
	return Part{apply: func(r *scriptResponse) error {
		if len(r.calls) == 0 || id == "" {
			return errors.New("CallID requires a preceding tool call and a non-empty ID")
		}
		r.calls[len(r.calls)-1].ID = id
		return nil
	}}
}

// Usage sets uncached input and output token counts; model attribution comes from the request.
func Usage(input, output int) Part {
	return Part{apply: func(r *scriptResponse) error {
		r.usage = model.TokenUsage{InputTokens: input, OutputTokens: output}
		if err := model.ValidateTokenUsage(r.usage); err != nil {
			return err
		}
		r.usage.TotalTokens = input + output
		return nil
	}}
}

// StopReason overrides the default tool_use or end_turn stop reason.
func StopReason(reason string) Part {
	return Part{apply: func(r *scriptResponse) error {
		r.stop = reason
		return nil
	}}
}

// OutputLimited marks output as incomplete; the validated client rejects it.
func OutputLimited() Part {
	return Part{apply: func(r *scriptResponse) error {
		r.limited = true
		return nil
	}}
}

// Fail returns err from Complete or from streaming setup.
func Fail(err error) Turn {
	if err == nil {
		panic("agenttest: Fail requires an error")
	}
	return Turn{failure: err, fragments: 1}
}

// ProviderFailure constructs a classified scripted provider failure.
func ProviderFailure(kind model.ProviderErrorKind, message string) Turn {
	status, retryable := providerFailureStatus(kind)
	return Fail(model.NewProviderError("scripted", "request", status, kind, "", message, "", retryable, nil))
}

// ContextOverflow constructs a non-retryable provider context overflow.
func ContextOverflow() Turn {
	return ProviderFailure(model.ProviderErrorKindContextOverflow, "scripted context overflow")
}

// When requires every matcher to pass before this turn can be consumed.
func (t Turn) When(matches ...Match) Turn {
	t.matches = append(slices.Clone(t.matches), matches...)
	return t
}

// Chunked splits each text part into n Unicode-safe fragments.
func (t Turn) Chunked(n int) Turn {
	if n <= 0 {
		panic("agenttest: Chunked requires n > 0")
	}
	t.fragments = n
	return t
}

// FailStreamAfter makes Recv fail after n emitted chunks; Complete is unaffected.
// NewScript rejects n greater than the turn's total chunk count.
func (t Turn) FailStreamAfter(n int, err error) Turn {
	if n < 0 || err == nil {
		panic("agenttest: FailStreamAfter requires n >= 0 and an error")
	}
	t.streamAfter = n
	t.streamFailure = err
	return t
}

func providerFailureStatus(kind model.ProviderErrorKind) (int, bool) {
	switch kind {
	case model.ProviderErrorKindAuth:
		return 401, false
	case model.ProviderErrorKindInvalidRequest, model.ProviderErrorKindContextOverflow:
		return 400, false
	case model.ProviderErrorKindRateLimited:
		return 429, true
	case model.ProviderErrorKindUnavailable:
		return 503, true
	case model.ProviderErrorKindUnknown:
		return 500, false
	default:
		panic(fmt.Sprintf("agenttest: unsupported provider failure kind %q", kind))
	}
}
