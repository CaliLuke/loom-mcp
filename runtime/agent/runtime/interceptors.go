package runtime

import (
	"context"
	"errors"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
)

type (
	// Interceptor observes or changes runtime call-path behavior.
	//
	// Interceptors are executed in registration order. They are separate from
	// hooks: hooks publish durable observability events, while interceptors run
	// inline before or after execution decisions are made.
	Interceptor interface{}

	// ToolInterceptor observes or changes tool execution behavior.
	ToolInterceptor interface {
		BeforeTool(context.Context, *BeforeToolInput) (*BeforeToolDecision, error)
		AfterTool(context.Context, *AfterToolInput) (*AfterToolDecision, error)
	}

	// RunInterceptor observes or changes run lifecycle behavior.
	RunInterceptor interface {
		BeforeRun(context.Context, *BeforeRunInput) (*BeforeRunDecision, error)
		AfterRun(context.Context, *AfterRunInput) (*AfterRunDecision, error)
	}

	// ModelInterceptor observes or changes model invocation behavior.
	ModelInterceptor interface {
		BeforeModel(context.Context, *BeforeModelInput) (*BeforeModelDecision, error)
		AfterModel(context.Context, *AfterModelInput) (*AfterModelDecision, error)
	}

	// EventInterceptor observes or changes hook event publication behavior.
	EventInterceptor interface {
		BeforeEvent(context.Context, *BeforeEventInput) (*BeforeEventDecision, error)
		AfterEvent(context.Context, *AfterEventInput) (*AfterEventDecision, error)
	}

	// RuntimeInterceptorFuncs adapts function fields into typed interceptors.
	RuntimeInterceptorFuncs struct {
		BeforeRunFunc   func(context.Context, *BeforeRunInput) (*BeforeRunDecision, error)
		AfterRunFunc    func(context.Context, *AfterRunInput) (*AfterRunDecision, error)
		BeforeToolFunc  func(context.Context, *BeforeToolInput) (*BeforeToolDecision, error)
		AfterToolFunc   func(context.Context, *AfterToolInput) (*AfterToolDecision, error)
		BeforeModelFunc func(context.Context, *BeforeModelInput) (*BeforeModelDecision, error)
		AfterModelFunc  func(context.Context, *AfterModelInput) (*AfterModelDecision, error)
		BeforeEventFunc func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error)
		AfterEventFunc  func(context.Context, *AfterEventInput) (*AfterEventDecision, error)
	}

	// ToolInterceptorFuncs adapts function fields into an Interceptor.
	ToolInterceptorFuncs struct {
		BeforeToolFunc func(context.Context, *BeforeToolInput) (*BeforeToolDecision, error)
		AfterToolFunc  func(context.Context, *AfterToolInput) (*AfterToolDecision, error)
	}

	// BeforeRunInput is passed before workflow run events are published.
	BeforeRunInput struct {
		AgentID    agent.Ident
		RunID      string
		Input      RunInput
		RunContext run.Context
	}

	// BeforeRunDecision changes the run input before execution begins.
	BeforeRunDecision struct {
		Input *RunInput
	}

	// AfterRunInput is passed after workflow execution produces an outcome.
	AfterRunInput struct {
		AgentID    agent.Ident
		RunID      string
		Input      RunInput
		Output     *RunOutput
		Err        error
		RunContext run.Context
	}

	// AfterRunDecision changes the run outcome. Set Output to replace the run
	// output; when Output is set the decision's Err (including nil) also
	// replaces the current run error. A decision with nil Output and nil Err
	// is an observer no-op and leaves the current outcome, including any run
	// error, unchanged.
	AfterRunDecision struct {
		Output *RunOutput
		Err    error
	}

	// BeforeToolInput is passed to interceptors immediately before a tool
	// executor runs.
	BeforeToolInput struct {
		Call planner.ToolRequest
		Meta ToolCallMeta
	}

	// BeforeToolDecision changes the tool request or returns a result without
	// invoking the registered executor.
	BeforeToolDecision struct {
		Payload rawjson.Message
		Result  *ToolExecutionResult
	}

	// AfterToolInput is passed to interceptors after a tool executor returns.
	AfterToolInput struct {
		Call      planner.ToolRequest
		Meta      ToolCallMeta
		Result    *planner.ToolResult
		Execution *ToolExecutionResult
		Err       error
		Duration  time.Duration
	}

	// AfterToolDecision changes the executor outcome. Set Execution when the
	// interceptor needs to replace the full runtime envelope, or Result when only
	// the planner-visible tool result changes.
	AfterToolDecision struct {
		Execution *ToolExecutionResult
		Result    *planner.ToolResult
		Err       error
	}

	// BeforeModelInput is passed immediately before a model client invocation.
	BeforeModelInput struct {
		AgentID   agent.Ident
		RunID     string
		SessionID string
		TurnID    string
		ModelID   string
		Request   *model.Request
	}

	// BeforeModelDecision changes the model request or short-circuits with a response.
	BeforeModelDecision struct {
		Request  *model.Request
		Response *model.Response
		Err      error
	}

	// AfterModelInput is passed after a model client invocation returns.
	AfterModelInput struct {
		AgentID   agent.Ident
		RunID     string
		SessionID string
		TurnID    string
		ModelID   string
		Request   *model.Request
		Response  *model.Response
		Err       error
	}

	// AfterModelDecision changes the model response or error.
	AfterModelDecision struct {
		Response *model.Response
		Err      error
	}

	// BeforeEventInput is passed before hook events are persisted or published.
	BeforeEventInput struct {
		Event   hooks.Event
		Payload []byte
		Input   HookActivityInput
	}

	// BeforeEventDecision changes or drops hook event publication.
	BeforeEventDecision struct {
		Event   hooks.Event
		Payload []byte
		Drop    bool
	}

	// AfterEventInput is passed after hook event publication succeeds or fails.
	AfterEventInput struct {
		Event   hooks.Event
		Dropped bool
		Err     error
	}

	// AfterEventDecision changes the event publication error. Only a non-nil
	// Err is adopted; an empty decision is an observer no-op that preserves
	// the current publication error, so canonical run-log append failures
	// cannot be cleared by observer interceptors.
	AfterEventDecision struct {
		Err error
	}
)

// BeforeRun implements RunInterceptor.
func (f RuntimeInterceptorFuncs) BeforeRun(ctx context.Context, input *BeforeRunInput) (*BeforeRunDecision, error) {
	if f.BeforeRunFunc == nil {
		return nil, nil
	}
	return f.BeforeRunFunc(ctx, input)
}

// AfterRun implements RunInterceptor.
func (f RuntimeInterceptorFuncs) AfterRun(ctx context.Context, input *AfterRunInput) (*AfterRunDecision, error) {
	if f.AfterRunFunc == nil {
		return nil, nil
	}
	return f.AfterRunFunc(ctx, input)
}

// BeforeTool implements ToolInterceptor.
func (f RuntimeInterceptorFuncs) BeforeTool(ctx context.Context, input *BeforeToolInput) (*BeforeToolDecision, error) {
	if f.BeforeToolFunc == nil {
		return nil, nil
	}
	return f.BeforeToolFunc(ctx, input)
}

// AfterTool implements ToolInterceptor.
func (f RuntimeInterceptorFuncs) AfterTool(ctx context.Context, input *AfterToolInput) (*AfterToolDecision, error) {
	if f.AfterToolFunc == nil {
		return nil, nil
	}
	return f.AfterToolFunc(ctx, input)
}

// BeforeModel implements ModelInterceptor.
func (f RuntimeInterceptorFuncs) BeforeModel(ctx context.Context, input *BeforeModelInput) (*BeforeModelDecision, error) {
	if f.BeforeModelFunc == nil {
		return nil, nil
	}
	return f.BeforeModelFunc(ctx, input)
}

// AfterModel implements ModelInterceptor.
func (f RuntimeInterceptorFuncs) AfterModel(ctx context.Context, input *AfterModelInput) (*AfterModelDecision, error) {
	if f.AfterModelFunc == nil {
		return nil, nil
	}
	return f.AfterModelFunc(ctx, input)
}

// BeforeEvent implements EventInterceptor.
func (f RuntimeInterceptorFuncs) BeforeEvent(ctx context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
	if f.BeforeEventFunc == nil {
		return nil, nil
	}
	return f.BeforeEventFunc(ctx, input)
}

// AfterEvent implements EventInterceptor.
func (f RuntimeInterceptorFuncs) AfterEvent(ctx context.Context, input *AfterEventInput) (*AfterEventDecision, error) {
	if f.AfterEventFunc == nil {
		return nil, nil
	}
	return f.AfterEventFunc(ctx, input)
}

// BeforeTool implements Interceptor.
func (f ToolInterceptorFuncs) BeforeTool(ctx context.Context, input *BeforeToolInput) (*BeforeToolDecision, error) {
	if f.BeforeToolFunc == nil {
		return nil, nil
	}
	return f.BeforeToolFunc(ctx, input)
}

// AfterTool implements Interceptor.
func (f ToolInterceptorFuncs) AfterTool(ctx context.Context, input *AfterToolInput) (*AfterToolDecision, error) {
	if f.AfterToolFunc == nil {
		return nil, nil
	}
	return f.AfterToolFunc(ctx, input)
}

func runBeforeToolInterceptors(ctx context.Context, interceptors []Interceptor, call planner.ToolRequest) (planner.ToolRequest, *ToolExecutionResult, error) {
	meta := toolCallMeta(call)
	for _, interceptor := range interceptors {
		toolInterceptor, ok := interceptor.(ToolInterceptor)
		if !ok || toolInterceptor == nil {
			continue
		}
		decision, err := toolInterceptor.BeforeTool(ctx, &BeforeToolInput{
			Call: call,
			Meta: meta,
		})
		if err != nil {
			return call, nil, err
		}
		if decision == nil {
			continue
		}
		if len(decision.Payload) > 0 {
			call.Payload = decision.Payload
			meta = toolCallMeta(call)
		}
		if decision.Result != nil {
			return call, decision.Result, nil
		}
	}
	return call, nil, nil
}

func runAfterToolInterceptors(
	ctx context.Context,
	interceptors []Interceptor,
	call planner.ToolRequest,
	exec *ToolExecutionResult,
	execErr error,
	duration time.Duration,
) (*ToolExecutionResult, error) {
	meta := toolCallMeta(call)
	current := exec
	currentErr := execErr
	for _, interceptor := range interceptors {
		toolInterceptor, ok := interceptor.(ToolInterceptor)
		if !ok || toolInterceptor == nil {
			continue
		}
		var result *planner.ToolResult
		if current != nil {
			result = current.ToolResult
		}
		decision, err := toolInterceptor.AfterTool(ctx, &AfterToolInput{
			Call:      call,
			Meta:      meta,
			Result:    result,
			Execution: current,
			Err:       currentErr,
			Duration:  duration,
		})
		if err != nil {
			return nil, err
		}
		if decision == nil {
			continue
		}
		var decisionErr error
		current, decisionErr = applyAfterToolDecision(current, decision)
		if decisionErr != nil || decision.Execution != nil || decision.Result != nil {
			currentErr = decisionErr
		}
	}
	if currentErr != nil {
		return nil, currentErr
	}
	if current == nil {
		return nil, errors.New("tool execution returned nil execution result")
	}
	return current, nil
}

func runBeforeRunInterceptors(ctx context.Context, interceptors []Interceptor, input RunInput, runCtx run.Context) (RunInput, error) {
	current := input
	for _, interceptor := range interceptors {
		runInterceptor, ok := interceptor.(RunInterceptor)
		if !ok || runInterceptor == nil {
			continue
		}
		decision, err := runInterceptor.BeforeRun(ctx, &BeforeRunInput{
			AgentID:    current.AgentID,
			RunID:      current.RunID,
			Input:      current,
			RunContext: runCtx,
		})
		if err != nil {
			return current, err
		}
		if decision != nil && decision.Input != nil {
			current = *decision.Input
		}
	}
	return current, nil
}

func runAfterRunInterceptors(ctx context.Context, interceptors []Interceptor, input RunInput, runCtx run.Context, out *RunOutput, runErr error) (*RunOutput, error) {
	currentOut := out
	currentErr := runErr
	for _, interceptor := range interceptors {
		runInterceptor, ok := interceptor.(RunInterceptor)
		if !ok || runInterceptor == nil {
			continue
		}
		decision, err := runInterceptor.AfterRun(ctx, &AfterRunInput{
			AgentID:    input.AgentID,
			RunID:      input.RunID,
			Input:      input,
			Output:     currentOut,
			Err:        currentErr,
			RunContext: runCtx,
		})
		if err != nil {
			return currentOut, err
		}
		if decision == nil {
			continue
		}
		if decision.Output != nil {
			currentOut = decision.Output
		}
		if decision.Output != nil || decision.Err != nil {
			currentErr = decision.Err
		}
	}
	return currentOut, currentErr
}

func runBeforeEventInterceptors(ctx context.Context, interceptors []Interceptor, evt hooks.Event, payload []byte, input HookActivityInput) (hooks.Event, []byte, bool, error) {
	currentEvent := evt
	currentPayload := payload
	for _, interceptor := range interceptors {
		eventInterceptor, ok := interceptor.(EventInterceptor)
		if !ok || eventInterceptor == nil {
			continue
		}
		decision, err := eventInterceptor.BeforeEvent(ctx, &BeforeEventInput{
			Event:   currentEvent,
			Payload: append([]byte(nil), currentPayload...),
			Input:   input,
		})
		if err != nil {
			return currentEvent, currentPayload, false, err
		}
		if decision == nil {
			continue
		}
		if decision.Drop {
			return currentEvent, currentPayload, true, nil
		}
		if decision.Event != nil {
			currentEvent = decision.Event
		}
		if decision.Payload != nil {
			currentPayload = append([]byte(nil), decision.Payload...)
		}
	}
	return currentEvent, currentPayload, false, nil
}

func runAfterEventInterceptors(ctx context.Context, interceptors []Interceptor, evt hooks.Event, dropped bool, eventErr error) error {
	currentErr := eventErr
	for _, interceptor := range interceptors {
		eventInterceptor, ok := interceptor.(EventInterceptor)
		if !ok || eventInterceptor == nil {
			continue
		}
		decision, err := eventInterceptor.AfterEvent(ctx, &AfterEventInput{
			Event:   evt,
			Dropped: dropped,
			Err:     currentErr,
		})
		if err != nil {
			return err
		}
		if decision != nil && decision.Err != nil {
			currentErr = decision.Err
		}
	}
	return currentErr
}

func applyAfterToolDecision(current *ToolExecutionResult, decision *AfterToolDecision) (*ToolExecutionResult, error) {
	switch {
	case decision.Execution != nil:
		return decision.Execution, decision.Err
	case decision.Result != nil:
		if current == nil {
			current = &ToolExecutionResult{}
		}
		current.ToolResult = decision.Result
		return current, decision.Err
	default:
		return current, decision.Err
	}
}
