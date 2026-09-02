package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"uuid"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
)

// runtimePlannerEvents implements planner.PlannerEvents for runtime plan
// activities.
//
// It serves two purposes:
//   - Publish hook events (streaming / persistence / observability) via the runtime bus.
//   - Capture thinking/text into a per-turn transcript ledger and aggregate token usage
//     for deterministic workflow consumption.
//
// The planner (or model wrapper) may emit events while streaming; methods therefore
// take a mutex to allow concurrent calls without corrupting the ledger or usage totals.
type runtimePlannerEvents struct {
	rt        *Runtime
	agentID   agent.Ident
	runID     string
	sessionID string
	turnID    string

	mu  sync.Mutex
	led *transcript.Ledger

	usage    model.TokenUsage
	usageErr error

	hookErr error

	presentations []pendingModelPresentation
}

type modelPresentationStatus uint8

type pendingModelPresentation struct {
	id       string
	messages []*model.Message
	status   modelPresentationStatus
}

const (
	modelPresentationStarted modelPresentationStatus = iota
	modelPresentationStaged
	modelPresentationReady
	modelPresentationCommitting
	modelPresentationAccepted
	modelPresentationDiscarded
)

// newPlannerEvents constructs a planner event sink that publishes to rt.Bus and
// records a provider transcript.
//
// The runtime requires a hook bus. If rt.Bus is nil, this panics to surface an
// invalid runtime configuration early.
func newPlannerEvents(rt *Runtime, agentID agent.Ident, runID, sessionID, turnID string) *runtimePlannerEvents {
	if rt == nil {
		panic("runtime: planner events runtime is nil")
	}
	if rt.Bus == nil {
		panic("runtime: planner events hook bus is nil")
	}
	return &runtimePlannerEvents{
		rt:        rt,
		agentID:   agentID,
		runID:     runID,
		sessionID: sessionID,
		turnID:    turnID,
		led:       transcript.NewLedger(),
	}
}

func (e *runtimePlannerEvents) AssistantChunk(ctx context.Context, text string) {
	if text == "" {
		return
	}
	e.mu.Lock()
	e.led.AppendText(text)
	e.mu.Unlock()
	e.publish(ctx, hooks.NewAssistantMessageEvent(e.runID, e.agentID, e.sessionID, text, nil))
}

func (e *runtimePlannerEvents) ToolCallArgsDelta(ctx context.Context, toolCallID string, toolName tools.Ident, delta string) {
	if toolCallID == "" || delta == "" {
		return
	}
	e.publish(ctx, hooks.NewToolCallArgsDeltaEvent(e.runID, e.agentID, e.sessionID, toolCallID, toolName, delta))
}

func (e *runtimePlannerEvents) PlannerThought(ctx context.Context, note string, labels map[string]string) {
	if note == "" {
		return
	}
	e.publish(ctx, hooks.NewPlannerNoteEvent(e.runID, e.agentID, e.sessionID, note, labels))
}

func (e *runtimePlannerEvents) UsageDelta(ctx context.Context, usage model.TokenUsage) {
	e.mu.Lock()
	if e.usageErr == nil {
		combined, err := checkedAddTokenUsage(e.usage, usage)
		if err != nil {
			e.usageErr = fmt.Errorf("aggregate planner usage: %w", err)
		} else {
			e.usage = combined
		}
	}
	e.mu.Unlock()

	e.publish(ctx, hooks.NewUsageEvent(e.runID, e.agentID, e.sessionID, usage))
}

func (e *runtimePlannerEvents) PlannerThinkingBlock(ctx context.Context, block model.ThinkingPart) {
	e.mu.Lock()
	e.led.AppendThinking(toTranscriptThinking(block))
	e.mu.Unlock()
	e.publish(ctx, hooks.NewThinkingBlockEvent(
		e.runID, e.agentID, e.sessionID,
		block.Text, block.Signature, block.Redacted, block.Index, block.Final,
	))
}

// CommitModelPresentation stages one validated model response. The planner
// activity commits it only after the planner itself returns successfully.
func (e *runtimePlannerEvents) CommitModelPresentation(_ context.Context, presentationID string, response *model.Response) error {
	if err := e.hookError(); err != nil {
		return err
	}
	owned, err := model.CloneResponse(response)
	if err != nil {
		return fmt.Errorf("clone model presentation response: %w", err)
	}
	if owned == nil {
		return errors.New("model presentation response is required")
	}
	messages := make([]*model.Message, len(owned.Content))
	for index := range owned.Content {
		messages[index] = &owned.Content[index]
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for index := range e.presentations {
		presentation := &e.presentations[index]
		if presentation.id != presentationID {
			continue
		}
		if presentation.status != modelPresentationStarted {
			return fmt.Errorf("model presentation %q cannot stage from status %d", presentationID, presentation.status)
		}
		if err := e.validatePresentationCommitSize(presentationID, messages); err != nil {
			return err
		}
		presentation.messages = messages
		presentation.status = modelPresentationStaged
		return nil
	}
	return fmt.Errorf("model presentation %q was not started", presentationID)
}

func (e *runtimePlannerEvents) validatePresentationCommitSize(candidateID string, candidateMessages []*model.Message) error {
	presentationIDs := make([]string, 0, len(e.presentations))
	messages := make([]*model.Message, 0, len(candidateMessages))
	for _, presentation := range e.presentations {
		if presentation.id == candidateID {
			presentationIDs = append(presentationIDs, candidateID)
			messages = append(messages, candidateMessages...)
			continue
		}
		if presentation.status != modelPresentationStaged &&
			presentation.status != modelPresentationReady &&
			presentation.status != modelPresentationCommitting {
			continue
		}
		presentationIDs = append(presentationIDs, presentation.id)
		messages = append(messages, presentation.messages...)
	}
	event := hooks.NewAssistantPresentationCommittedEvent(e.runID, e.agentID, e.sessionID, presentationIDs, messages)
	input, err := hooks.EncodeToHookInput(event, e.turnID)
	if err != nil {
		return fmt.Errorf("encode model presentation commit: %w", err)
	}
	if len(input.Payload) > maxCanonicalPresentationPayloadBytes {
		return fmt.Errorf(
			"model presentation commit exceeds maximum size %d bytes",
			maxCanonicalPresentationPayloadBytes,
		)
	}
	return nil
}

// StartModelPresentation begins one best-effort provisional stream lifecycle.
func (e *runtimePlannerEvents) StartModelPresentation(ctx context.Context) string {
	presentationID := uuid.New().String()
	e.mu.Lock()
	e.presentations = append(e.presentations, pendingModelPresentation{
		id:     presentationID,
		status: modelPresentationStarted,
	})
	e.mu.Unlock()
	payload := stream.ModelPresentationPayload{
		PresentationID: presentationID,
		State:          stream.ModelPresentationStarted,
	}
	e.rt.publishModelPresentation(ctx, e.sessionID, stream.ModelPresentation{
		Base: stream.NewBase(stream.EventModelPresentation, e.runID, e.sessionID, payload),
		Data: payload,
	})
	return presentationID
}

// PublishModelText emits provisional model text without durable hooks.
func (e *runtimePlannerEvents) PublishModelText(ctx context.Context, presentationID, text string) {
	if text == "" {
		return
	}
	payload := stream.AssistantReplyPayload{Text: text}
	payload.PresentationID = presentationID
	e.rt.publishModelPresentation(ctx, e.sessionID, stream.AssistantReply{
		Base: stream.NewBase(stream.EventAssistantReply, e.runID, e.sessionID, payload),
		Data: payload,
	})
}

// PublishModelThinking emits provisional model thinking without durable hooks.
func (e *runtimePlannerEvents) PublishModelThinking(ctx context.Context, presentationID string, block model.ThinkingPart) {
	payload := stream.PlannerThoughtPayload{
		PresentationID: presentationID,
		Text:           block.Text,
		Signature:      block.Signature,
		Redacted:       append([]byte(nil), block.Redacted...),
		ContentIndex:   block.Index,
		Final:          block.Final,
	}
	e.rt.publishModelPresentation(ctx, e.sessionID, stream.PlannerThought{
		Base: stream.NewBase(stream.EventPlannerThought, e.runID, e.sessionID, payload),
		Data: payload,
	})
}

// FinishModelPresentation records validation success for activity-owned commit,
// or immediately discards a rejected stream.
func (e *runtimePlannerEvents) FinishModelPresentation(ctx context.Context, presentationID string, accepted bool) {
	e.mu.Lock()
	for index := range e.presentations {
		presentation := &e.presentations[index]
		if presentation.id != presentationID {
			continue
		}
		if accepted && presentation.status == modelPresentationStaged {
			presentation.status = modelPresentationReady
			e.mu.Unlock()
			return
		}
		if !accepted && presentation.status != modelPresentationAccepted && presentation.status != modelPresentationDiscarded {
			presentation.status = modelPresentationDiscarded
			e.mu.Unlock()
			e.publishModelPresentationState(ctx, presentationID, stream.ModelPresentationDiscarded)
			return
		}
		break
	}
	e.mu.Unlock()
}

func (e *runtimePlannerEvents) publishModelPresentationState(ctx context.Context, presentationID string, state stream.ModelPresentationState) {
	payload := stream.ModelPresentationPayload{PresentationID: presentationID, State: state}
	e.rt.publishModelPresentation(context.WithoutCancel(ctx), e.sessionID, stream.ModelPresentation{
		Base: stream.NewBase(stream.EventModelPresentation, e.runID, e.sessionID, payload),
		Data: payload,
	})
}

func (e *runtimePlannerEvents) commitModelPresentations(ctx context.Context) error {
	presentationIDs, messages, err := e.prepareModelPresentationCommit()
	if err != nil {
		e.discardModelPresentations(ctx)
		return err
	}
	if len(presentationIDs) == 0 {
		return nil
	}

	committed := hooks.NewAssistantPresentationCommittedEvent(e.runID, e.agentID, e.sessionID, presentationIDs, messages)
	e.publish(ctx, committed)
	if err := e.hookError(); err != nil {
		e.discardModelPresentations(ctx)
		return err
	}
	e.acceptCommittingModelPresentations()
	for _, presentationID := range presentationIDs {
		e.publishModelPresentationState(ctx, presentationID, stream.ModelPresentationAccepted)
	}
	return nil
}

func (e *runtimePlannerEvents) prepareModelPresentationCommit() ([]string, []*model.Message, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	unfinalizedID := findUnfinalizedModelPresentation(e.presentations)
	if unfinalizedID != "" {
		return nil, nil, fmt.Errorf("model presentation %q was not finalized", unfinalizedID)
	}
	var presentationIDs []string
	var messages []*model.Message
	for index := range e.presentations {
		presentation := &e.presentations[index]
		if presentation.status != modelPresentationReady {
			continue
		}
		presentation.status = modelPresentationCommitting
		presentationIDs = append(presentationIDs, presentation.id)
		messages = append(messages, presentation.messages...)
	}
	return presentationIDs, messages, nil
}

func (e *runtimePlannerEvents) acceptCommittingModelPresentations() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for index := range e.presentations {
		presentation := &e.presentations[index]
		if presentation.status != modelPresentationCommitting {
			continue
		}
		for _, message := range presentation.messages {
			e.led.AppendAssistantMessage(message)
		}
		presentation.status = modelPresentationAccepted
	}
}

func findUnfinalizedModelPresentation(presentations []pendingModelPresentation) string {
	for _, presentation := range presentations {
		switch presentation.status {
		case modelPresentationStarted, modelPresentationStaged:
			return presentation.id
		case modelPresentationReady, modelPresentationCommitting, modelPresentationAccepted, modelPresentationDiscarded:
			continue
		default:
			panic(fmt.Sprintf("runtime: unknown model presentation status %d", presentation.status))
		}
	}
	return ""
}

func (e *runtimePlannerEvents) discardModelPresentations(ctx context.Context) {
	e.mu.Lock()
	ids := make([]string, 0, len(e.presentations))
	for index := range e.presentations {
		presentation := &e.presentations[index]
		if presentation.status == modelPresentationAccepted || presentation.status == modelPresentationDiscarded {
			continue
		}
		presentation.status = modelPresentationDiscarded
		ids = append(ids, presentation.id)
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.publishModelPresentationState(ctx, id, stream.ModelPresentationDiscarded)
	}
}

func (e *runtimePlannerEvents) presentationOwnsFinalResponse(result *planner.PlanResult) bool {
	if result == nil || result.FinalResponse == nil {
		return false
	}
	want := agentMessageText(result.FinalResponse.Message)
	if want == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for index := len(e.presentations) - 1; index >= 0; index-- {
		presentation := e.presentations[index]
		if presentation.status != modelPresentationAccepted {
			continue
		}
		if transcriptText(presentation.messages) == want {
			return true
		}
	}
	return false
}

func (e *runtimePlannerEvents) exportTranscript() []*model.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.led.BuildMessages()
}

func (e *runtimePlannerEvents) exportUsage() model.TokenUsage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage
}

func (e *runtimePlannerEvents) hookError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return errors.Join(e.hookErr, e.usageErr)
}

func (e *runtimePlannerEvents) publish(ctx context.Context, evt hooks.Event) {
	if e.hookError() != nil {
		return
	}
	if err := e.rt.publishHookErr(ctx, evt, e.turnID); err != nil {
		e.mu.Lock()
		if e.hookErr == nil {
			e.hookErr = err
		}
		e.mu.Unlock()
	}
}

func toTranscriptThinking(block model.ThinkingPart) transcript.ThinkingPart {
	cp := transcript.ThinkingPart{
		Text:      block.Text,
		Signature: block.Signature,
		Index:     block.Index,
		Final:     block.Final,
	}
	if len(block.Redacted) > 0 {
		cp.Redacted = append([]byte(nil), block.Redacted...)
	}
	return cp
}
