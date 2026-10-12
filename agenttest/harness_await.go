package agenttest

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Responder answers awaits using the first matching rule in declaration order.
// Callbacks may run concurrently for different runs and must return promptly.
type Responder []AwaitRule

// AwaitRule selects and constructs one kind of external-input answer.
// Construct rules using Approve, Deny, Clarify, Answer, TypedInput or callbacks.
type AwaitRule struct {
	answer func(hooks.Event) (any, bool, error)
}

type awaitWorker struct {
	mu           sync.Mutex
	subscription hooks.Subscription
	open         bool
	cancel       context.CancelFunc
	done         chan struct{}
	failures     chan error
	answers      []AnsweredAwait
}

// ErrUnansweredAwait indicates that no responder rule matched an observed await.
var ErrUnansweredAwait = errors.New("agenttest: unanswered await")

var errResponderAlive = errors.New("responder still alive")

// WithResponder configures the rules used by every run, including session turns.
func WithResponder(responder Responder) Option {
	return func(options *harnessOptions) error {
		options.responder = slices.Clone(responder)
		return nil
	}
}

// Approve approves confirmations for the named tools; no tools matches all.
func Approve(tools ...tools.Named) AwaitRule {
	return confirmationRule(true, tools)
}

// Deny denies confirmations for the named tools; no tools matches all.
func Deny(tools ...tools.Named) AwaitRule {
	return confirmationRule(false, tools)
}

// Clarify answers every clarification with the supplied text.
func Clarify(answer string) AwaitRule {
	return ClarifyFunc(func(*hooks.AwaitClarificationEvent) (string, error) {
		return answer, nil
	})
}

// ClarifyFunc derives a clarification answer from its observed prompt.
func ClarifyFunc(answer func(*hooks.AwaitClarificationEvent) (string, error)) AwaitRule {
	return AwaitRule{answer: func(event hooks.Event) (any, bool, error) {
		await, ok := event.(*hooks.AwaitClarificationEvent)
		if !ok {
			return nil, false, nil
		}
		text, err := answer(await)
		return &api.ClarificationAnswer{RunID: event.RunID(), ID: await.ID, Answer: text}, true, err
	}}
}

// Answer selects option IDs for a matching question. The JSON result is
// {"answers":[{"question_id":"...","selected_ids":["..."]}]}.
// Use QuestionsFunc when one await needs multiple question answers or a custom schema.
func Answer(questionID string, optionIDs ...string) AwaitRule {
	selected := slices.Clone(optionIDs)
	return AwaitRule{answer: func(event hooks.Event) (any, bool, error) {
		await, ok := event.(*hooks.AwaitQuestionsEvent)
		if !ok {
			return nil, false, nil
		}
		for _, question := range await.Questions {
			if question.ID == questionID {
				payload, err := json.Marshal(map[string]any{"answers": []map[string]any{{"question_id": questionID, "selected_ids": selected}}})
				return questionAnswer(await, payload), true, err
			}
		}
		return nil, false, nil
	}}
}

// QuestionsFunc returns the application-owned JSON result for a questions tool.
func QuestionsFunc(answer func(*hooks.AwaitQuestionsEvent) (rawjson.Message, error)) AwaitRule {
	return AwaitRule{answer: func(event hooks.Event) (any, bool, error) {
		await, ok := event.(*hooks.AwaitQuestionsEvent)
		if !ok {
			return nil, false, nil
		}
		payload, err := answer(await)
		return questionAnswer(await, payload), true, err
	}}
}

// TypedInput answers typed-input awaits with an owned copy of the JSON payload.
func TypedInput(payload rawjson.Message) AwaitRule {
	owned := slices.Clone(payload)
	return TypedInputFunc(func(*hooks.AwaitTypedInputEvent) (rawjson.Message, error) {
		return slices.Clone(owned), nil
	})
}

// TypedInputFunc derives a typed-input payload from the await schema and title.
func TypedInputFunc(answer func(*hooks.AwaitTypedInputEvent) (rawjson.Message, error)) AwaitRule {
	return AwaitRule{answer: func(event hooks.Event) (any, bool, error) {
		await, ok := event.(*hooks.AwaitTypedInputEvent)
		if !ok {
			return nil, false, nil
		}
		payload, err := answer(await)
		return &api.TypedInputAnswer{RunID: event.RunID(), ID: await.ID, Payload: payload}, true, err
	}}
}

// ExternalToolsFunc constructs the wire-safe results for an external-tools await.
func ExternalToolsFunc(answer func(*hooks.AwaitExternalToolsEvent) ([]*api.ProvidedToolResult, error)) AwaitRule {
	return AwaitRule{answer: func(event hooks.Event) (any, bool, error) {
		await, ok := event.(*hooks.AwaitExternalToolsEvent)
		if !ok {
			return nil, false, nil
		}
		results, err := answer(await)
		return &api.ToolResultsSet{RunID: event.RunID(), ID: await.ID, Results: results}, true, err
	}}
}

func confirmationRule(approved bool, names []tools.Named) AwaitRule {
	ids := make([]tools.Ident, len(names))
	for i, name := range names {
		ids[i] = name.Ident()
	}
	return AwaitRule{answer: func(event hooks.Event) (any, bool, error) {
		await, ok := event.(*hooks.AwaitConfirmationEvent)
		if !ok || (len(ids) > 0 && !slices.Contains(ids, await.ToolName)) {
			return nil, false, nil
		}
		return &api.ConfirmationDecision{RunID: event.RunID(), ID: await.ID, Approved: approved, RequestedBy: "agenttest"}, true, nil
	}}
}

func questionAnswer(await *hooks.AwaitQuestionsEvent, payload rawjson.Message) *api.ToolResultsSet {
	return &api.ToolResultsSet{RunID: await.RunID(), ID: await.ID, Results: []*api.ProvidedToolResult{{Name: await.ToolName, ToolCallID: await.ToolCallID, Result: payload}}}
}

// awaitIdentity admits only await events and supplies useful unmatched diagnostics.
func awaitIdentity(event hooks.Event) (id, prompt string, ok bool) {
	switch await := event.(type) {
	case *hooks.AwaitClarificationEvent:
		return await.ID, await.Question, true
	case *hooks.AwaitConfirmationEvent:
		return await.ID, fmt.Sprintf("%s: %s", await.ToolName, await.Prompt), true
	case *hooks.AwaitQuestionsEvent:
		return await.ID, string(await.ToolName), true
	case *hooks.AwaitTypedInputEvent:
		return await.ID, await.Title, true
	case *hooks.AwaitExternalToolsEvent:
		return await.ID, fmt.Sprint(await.Items), true
	default:
		return "", "", false
	}
}

func sendAnswer(ctx context.Context, rt *runtime.Runtime, answer any) error {
	switch value := answer.(type) {
	case *api.ConfirmationDecision:
		return rt.ProvideConfirmation(ctx, value)
	case *api.ClarificationAnswer:
		return rt.ProvideClarification(ctx, value)
	case *api.TypedInputAnswer:
		return rt.ProvideTypedInput(ctx, value)
	case *api.ToolResultsSet:
		return rt.ProvideToolResults(ctx, value)
	default:
		return fmt.Errorf("agenttest: invalid await answer %T", answer)
	}
}

func (h *Harness) subscribeAwaits(ctx context.Context, runID string) (*awaitWorker, error) {
	ctx, cancel := context.WithCancel(ctx)
	worker := &awaitWorker{cancel: cancel, done: make(chan struct{}), failures: make(chan error, 1)}
	events := make(chan hooks.Event, 64)
	var children sync.Map
	subscription, err := h.rt.Bus.Register(hooks.SubscriberFunc(func(_ context.Context, event hooks.Event) error {
		worker.observeAwait(ctx, runID, event, &children, events)
		return nil
	}))
	if err != nil {
		cancel()
		return nil, err
	}
	worker.subscription = subscription
	worker.open = true
	go func() {
		defer close(worker.done)
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-events:
				if err := worker.respond(ctx, h.rt, h.responder, event); err != nil {
					worker.fail(err)
					return
				}
			}
		}
	}()
	return worker, nil
}

func (w *awaitWorker) fail(err error) {
	select {
	case w.failures <- err:
	default:
	}
}

func (w *awaitWorker) respond(ctx context.Context, rt *runtime.Runtime, rules Responder, event hooks.Event) error {
	id, prompt, ok := awaitIdentity(event)
	if !ok {
		return errors.New("agenttest: unexpected non-await event")
	}
	for _, rule := range rules {
		answer, matched, err := rule.answer(event)
		if !matched {
			continue
		}
		if err == nil {
			err = sendAnswer(ctx, rt, answer)
		}
		if err != nil {
			return fmt.Errorf("agenttest: %s %q (%s): %w", event.Type(), id, prompt, err)
		}
		w.mu.Lock()
		w.answers = append(w.answers, AnsweredAwait{Kind: event.Type(), ID: id, Event: event, Answer: answer})
		w.mu.Unlock()
		return nil
	}
	return fmt.Errorf("%w: %s %q (%s)", ErrUnansweredAwait, event.Type(), id, prompt)
}

func (w *awaitWorker) close(ctx context.Context) error {
	w.cancel()
	w.mu.Lock()
	err := w.subscription.Close()
	if err == nil {
		w.open = false
	}
	w.mu.Unlock()
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	select {
	case <-w.done:
		return err
	case <-cleanup.Done():
		return errors.Join(err, fmt.Errorf("%w: %w", errResponderAlive, cleanup.Err()))
	}
}

func (w *awaitWorker) leaks(runID string) []error {
	var diagnostics []error
	w.mu.Lock()
	if w.open {
		diagnostics = append(diagnostics, fmt.Errorf("agenttest: run %s: bus subscription still open", runID))
	}
	w.mu.Unlock()
	return diagnostics
}

func (w *awaitWorker) answered() []AnsweredAwait {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.answers)
}

// alive reports whether the responder still owns its goroutine.
func (w *awaitWorker) alive() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

// observeAwait routes root awaits and diagnoses descendants without invoking
// responder callbacks on the synchronous hook bus.
func (w *awaitWorker) observeAwait(ctx context.Context, runID string, event hooks.Event, children *sync.Map, events chan<- hooks.Event) {
	if started, ok := event.(*hooks.RunStartedEvent); ok && started.RunContext.RootRunID == runID && event.RunID() != runID {
		children.Store(event.RunID(), true)
	}
	id, prompt, isAwait := awaitIdentity(event)
	if !isAwait {
		return
	}
	if event.RunID() != runID {
		if _, child := children.Load(event.RunID()); child {
			w.fail(fmt.Errorf("%w: child run %s: %s %q (%s)", ErrUnansweredAwait, event.RunID(), event.Type(), id, prompt))
		}
		return
	}
	select {
	case <-ctx.Done():
	case events <- event:
	default:
		w.fail(fmt.Errorf("agenttest: run %s: await buffer overflow", runID))
	}
}
