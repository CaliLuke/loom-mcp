package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// RequestGrowth identifies the first started call carrying more encoded
	// message bytes outside the recorded view than inside it.
	RequestGrowth = api.RequestGrowth

	// CallRecording owns call evidence for one activity attempt. Workflow state
	// owns run-level adoption and growth event publication.
	CallRecording struct {
		qualifications []Qualification
		store          runstore.Store
		scope          string
		ref            index.ViewRef
		view           []*model.Message
		viewErr        error
		step           int
		activity       callrecord.ActivityKind
		mu             sync.Mutex
		keys           []runstore.CallKey
		completed      []runstore.CallKey
		facts          map[runstore.CallKey]contextmeasure.RequestFacts
		responses      map[runstore.CallKey]runstore.Hash
		growth         *RequestGrowth
		streams        []*callRecordingStream
		failures       []error
		overflowStep   overflow.Step
		models         []api.ModelContext
		fingerprint    string
		guardMargin    contextmeasure.Rate
		triggerAt      contextmeasure.Rate
		retryTools     []*model.ToolDefinition
		marked         *contextmeasure.ContextOverflow
	}

	callRecordingClient struct {
		inner     model.Client
		recording *CallRecording
	}

	recordedCall struct {
		recording *CallRecording
		record    runstore.CallRecord
		started   time.Time
		facts     contextmeasure.RequestFacts
	}
)

// NewCallRecording snapshots the committed pre-policy view for an activity.
// A snapshot error rejects calls before sending. Step is the monotonic planner
// schedule number, independent of the conversational TurnID and engine Attempt.
func NewCallRecording(store runstore.Store, scope string, ref index.ViewRef, view []*model.Message, step int) *CallRecording {
	recording := &CallRecording{store: store, scope: scope, ref: ref, step: step, activity: callrecord.ActivityPlanner, facts: make(map[runstore.CallKey]contextmeasure.RequestFacts), responses: make(map[runstore.CallKey]runstore.Hash)}
	snapshot, err := callrecord.SnapshotMessages(view)
	if err != nil {
		recording.viewErr = fmt.Errorf("snapshot recorded view: %w", err)
	} else {
		recording.view = snapshot
	}
	return recording
}

// Overflow returns an owned copy of the attempt's durable rejection.
func (r *CallRecording) Overflow() *contextmeasure.ContextOverflow {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.marked == nil {
		return nil
	}
	copy := *r.marked
	return &copy
}

func (r *CallRecording) configureOverflow(input *PlanActivityInput) {
	if input.Overflow != nil {
		r.overflowStep = *input.Overflow
	}
	r.fingerprint = input.View.ContextFingerprint
	r.models = input.Models
	r.guardMargin = input.ContextSettings.GuardMargin
	r.triggerAt = input.ContextSettings.TriggerAt
	if recallEnabled(input, input.ContextSettings.DisableRecall) {
		r.retryTools = []*model.ToolDefinition{recallDefinition()}
	}
}

func (c *callRecordingClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	call, err := c.recording.begin(ctx, req, c.inner)
	if err != nil {
		return nil, err
	}
	response, primary := c.inner.Complete(ctx, req)
	if response == nil && primary == nil {
		primary = errors.New("model complete contract violation: nil response with nil error")
	}
	usage := model.TokenUsage{}
	if response != nil {
		usage = response.Usage
	} else {
		var rejection *model.OutputValidationError
		if errors.As(primary, &rejection) && rejection.Usage() != nil {
			usage = *rejection.Usage()
		}
	}
	completionErr := call.complete(context.WithoutCancel(ctx), response, usage, primary, "")
	if completionErr != nil {
		return nil, errors.Join(primary, completionErr)
	}
	return response, primary
}

func (c *callRecordingClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	call, err := c.recording.begin(ctx, req, c.inner)
	if err != nil {
		return nil, err
	}
	streamContext, cancel := context.WithCancel(ctx)
	inner, primary := c.inner.Stream(streamContext, req)
	if primary == nil && inner == nil {
		primary = errors.New("model stream contract violation: nil streamer with nil error")
	}
	if primary != nil {
		if inner != nil {
			primary = errors.Join(primary, inner.Finalize(primary))
		}
		cancel()
		return nil, errors.Join(primary, call.complete(context.WithoutCancel(ctx), nil, model.TokenUsage{}, primary, ""))
	}
	stream := &callRecordingStream{inner: inner, call: call, ctx: context.WithoutCancel(ctx), cancel: cancel}
	c.recording.mu.Lock()
	c.recording.streams = append(c.recording.streams, stream)
	c.recording.mu.Unlock()
	return stream, nil
}

// Keys returns every key successfully started by this attempt, in start order.
func (r *CallRecording) Keys() []runstore.CallKey {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.keys)
}

// Facts returns durable completions in the same start order as Keys. Finish must succeed before adoption.
func (r *CallRecording) Facts() []contextmeasure.RequestFacts {
	r.mu.Lock()
	defer r.mu.Unlock()
	facts := make([]contextmeasure.RequestFacts, 0, len(r.keys))
	for _, key := range r.keys {
		facts = append(facts, r.facts[key])
	}
	return facts
}

// RequestGrowth returns an owned copy of the first growth warning, or nil.
func (r *CallRecording) RequestGrowth() *RequestGrowth {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.growth == nil {
		return nil
	}
	growth := *r.growth
	return &growth
}

// Finish synchronously marks every still-open stream failed as stream_abandoned.
// Call on every activity exit, including failed and canceled attempts.
func (r *CallRecording) Finish(ctx context.Context) error {
	r.mu.Lock()
	streams := slices.Clone(r.streams)
	failures := slices.Clone(r.failures)
	r.mu.Unlock()
	for _, stream := range streams {
		if err := stream.abandon(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// ProducingCall returns the last successfully completed call that emitted planner
// events. All started keys are adopted independently of this content correlation.
func (r *CallRecording) ProducingCall(eventKeys []runstore.CallKey) *runstore.CallKey {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.completed) - 1; i >= 0; i-- {
		if slices.Contains(eventKeys, r.completed[i]) {
			key := r.completed[i]
			return &key
		}
	}
	return nil
}

func recordedCallError(err error, kind string) *callrecord.CallError {
	if kind == "" {
		kind = "unknown"
		var provider *model.ProviderError
		var validation *model.OutputValidationError
		switch {
		case errors.As(err, &validation):
			kind = string(validation.Kind())
		case errors.As(err, &provider):
			kind = string(provider.Kind())
		case errors.Is(err, context.Canceled):
			kind = "canceled"
		case errors.Is(err, context.DeadlineExceeded):
			kind = "deadline_exceeded"
		}
	}
	return &callrecord.CallError{Kind: kind, Message: err.Error()}
}

// begin validates identity and commits the request before the registered client runs.
func (r *CallRecording) begin(ctx context.Context, req *model.Request, registered model.Client) (*recordedCall, error) {
	info, err := r.recordingCallInfo(ctx)
	if err != nil {
		return nil, err
	}
	parts, sizes, err := callrecord.BuildRequest(ctx, r.store, r.scope, req, r.ref, r.view)
	if err != nil {
		return nil, fmt.Errorf("record model request: %w", err)
	}
	key := runstore.CallKey{RunID: info.RunID, Seq: info.Seq, Attempt: info.Attempt}
	facts, limits, err := r.measureRequest(ctx, req, registered, key, info.ModelID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	started := callrecord.Started{Context: r.fingerprint, Key: key, Activity: r.activity, TurnID: info.TurnID, Step: r.step, Request: parts, Started: now, Model: req.Model, ModelID: info.ModelID, Class: req.ModelClass, Prompts: req.PromptRefs}
	body, err := callrecord.Encode(started)
	if err != nil {
		return nil, err
	}
	record := runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: body}
	if err := r.store.PutCall(ctx, r.scope, record); err != nil {
		return nil, fmt.Errorf("write started model call: %w", err)
	}
	r.mu.Lock()
	r.keys = append(r.keys, key)
	r.facts[key] = facts
	if r.growth == nil && sizes.Outside > sizes.Inside {
		r.growth = &RequestGrowth{Key: key, Inside: sizes.Inside, Outside: sizes.Outside}
	}
	r.mu.Unlock()
	call := &recordedCall{recording: r, record: record, started: now, facts: facts}
	if err := r.checkOverflow(ctx, req, registered, call, limits); err != nil {
		return nil, err
	}
	return call, nil
}

func (c *recordedCall) complete(ctx context.Context, response *model.Response, usage model.TokenUsage, primary error, kind string) (writeErr error) {
	defer func() {
		if writeErr != nil {
			c.recording.mu.Lock()
			c.recording.failures = append(c.recording.failures, writeErr)
			c.recording.mu.Unlock()
		}
	}()
	facts := c.facts.WithResult(usage, primary)
	source := ""
	switch {
	case kind == "context_budget":
		facts.Outcome, source = contextmeasure.OutcomePreSendRejected, contextmeasure.OverflowGuard
	case kind == "context_overflow_no_progress":
		source = contextmeasure.OverflowNoProgress
	case facts.Outcome == contextmeasure.OutcomeOverflow:
		source = contextmeasure.OverflowProvider
	}
	completion := callrecord.Completion{Usage: usage, Latency: time.Since(c.started), Facts: &facts}
	var err error
	completion.Response, err = c.storeResponse(ctx, response)
	if err != nil {
		return err
	}
	state := runstore.CallCompleted
	if primary != nil {
		state = runstore.CallFailed
		completion.Error = recordedCallError(primary, kind)
	}
	data, err := callrecord.Encode(completion)
	if err != nil {
		return err
	}
	record := c.record
	record.State, record.Completion = state, data
	if err := c.recording.store.PutCall(ctx, c.recording.scope, record); err != nil {
		return fmt.Errorf("write model call completion: %w", err)
	}
	c.recording.adoptCompletion(record.Key, facts, source, primary, state == runstore.CallCompleted, completion.Response)
	return nil
}

// measureRequest snapshots facts before sending, with limits from the registered
// client rather than the outer planner wrappers that drop optional capabilities.
func (r *CallRecording) measureRequest(ctx context.Context, req *model.Request, registered model.Client, key runstore.CallKey, modelID string) (contextmeasure.RequestFacts, model.ContextLimits, error) {
	var limits model.ContextLimits
	resolved := req.Model
	if contextInfo, ok := registered.(model.ContextInfo); ok {
		var known bool
		limits, resolved, known = contextInfo.ContextLimits(ctx, req)
		if !known {
			limits.ContextWindow = 0
		}
	}
	facts, err := contextmeasure.Measure(req, limits, r.view)
	if err != nil {
		return contextmeasure.RequestFacts{}, limits, fmt.Errorf("measure model request: %w", err)
	}
	if facts.ViewBearing {
		facts.RecoveryToolBytes, err = recoveryToolBytes(req, r.retryTools)
		if err != nil {
			return contextmeasure.RequestFacts{}, limits, fmt.Errorf("measure recovery tools: %w", err)
		}
	}
	if info, ok := registered.(model.ToolResultPartsInfo); ok {
		facts.ToolResultParts = info.ToolResultParts(ctx, req)
	}
	facts.Call = key
	facts.Model = firstNonEmpty(resolved, req.Model, modelID)
	if info, ok := registered.(model.ProviderConfigurationInfo); ok {
		facts.ProviderConfig = info.ProviderConfiguration(ctx, req)
	}
	facts.Model = model.NormalizeProviderModel(facts.ProviderConfig, facts.Model)
	facts.Qualified = compaction.LookupQualification(r.qualifications, facts.ProviderConfig, facts.Model)
	if facts.Qualified == "" {
		facts.Qualified = compaction.QualifiedTier(facts.ProviderConfig, facts.Model)
	}
	return facts, limits, nil
}

// finishActivity preserves the activity error and rejects outputs after a failed write.
func (r *CallRecording) finishActivity(ctx context.Context, out **PlanActivityOutput, activityErr *error, events *runtimePlannerEvents) {
	if err := r.Finish(ctx); err != nil {
		*out = nil
		*activityErr = errors.Join(*activityErr, err)
		return
	}
	if *out != nil {
		ref := r.ref
		ref.Pinned = slices.Clone(ref.Pinned)
		(*out).CallView = &ref
		(*out).CallKeys = r.Keys()
		(*out).Facts = r.Facts()
		(*out).RequestGrowth = r.RequestGrowth()
		(*out).ProducingCall = r.ProducingCall(events.eventCallKeys())
	}
}

// checkOverflow runs only after the started record has been persisted.
func (r *CallRecording) checkOverflow(ctx context.Context, req *model.Request, registered model.Client, call *recordedCall, limits model.ContextLimits) error {
	rate, calibrated := contextmeasure.Rate{Tokens: 1, Bytes: 3}, false
	for _, adopted := range r.models {
		if adopted.Model == call.facts.Model {
			rate, calibrated = adopted.Rate, adopted.Calibrated
			break
		}
	}
	counter, _ := registered.(model.TokenCounter)
	budget := max(0, call.facts.ContextWindow-call.facts.OutputReserve)
	trigger := r.triggerAt
	if trigger.Bytes == 0 {
		trigger = contextmeasure.Rate{Tokens: 85, Bytes: 100}
	}
	count := contextmeasure.GuardTokens(ctx, counter, req, rate, calibrated, contextmeasure.GuardBudget{Tokens: budget, ImageTokens: limits.ImageTokens}, trigger)
	call.facts.CountLatency = count.Latency
	margin := r.guardMargin
	if margin.Bytes == 0 {
		margin = contextmeasure.Rate{Tokens: 1, Bytes: 10}
	}
	var rejection error
	kind := ""
	switch overflow.Check(r.overflowStep, call.facts, count, overflow.Limits{Budget: budget, GuardMargin: margin}) {
	case overflow.Send:
		return nil
	case overflow.GuardReject:
		rejection, kind = ErrContextBudget, "context_budget"
	case overflow.NoProgress:
		rejection, kind = ErrContextOverflowNoProgress, "context_overflow_no_progress"
	}
	if rejection != nil {
		return errors.Join(rejection, call.complete(context.WithoutCancel(ctx), nil, model.TokenUsage{}, rejection, kind))
	}
	return nil
}

// adoptCompletion marks overflow only after completion evidence is durable.
func (r *CallRecording) adoptCompletion(key runstore.CallKey, facts contextmeasure.RequestFacts, source string, primary error, completed bool, response *runstore.Hash) {
	r.mu.Lock()
	r.facts[key] = facts
	if response != nil {
		r.responses[key] = *response
	}
	if source != "" && r.marked == nil {
		ordinal := 0
		for _, startedKey := range r.keys {
			if startedKey == key || r.facts[startedKey].Model == facts.Model {
				ordinal++
			}
			if startedKey == key {
				break
			}
		}
		r.marked = &contextmeasure.ContextOverflow{Source: source, Failed: facts, Ordinal: ordinal, ViewTextBytes: facts.ViewTextBytes}
		if provider, ok := model.AsProviderError(primary); ok && source == contextmeasure.OverflowProvider {
			r.marked.Provider = contextmeasure.CaptureProviderFailure(provider)
		}
	}
	r.mu.Unlock()
	if completed {
		r.mu.Lock()
		r.completed = append(r.completed, key)
		r.mu.Unlock()
	}
}

func (r *CallRecording) recordingCallInfo(ctx context.Context) (model.CallInfo, error) {
	r.mu.Lock()
	pending := r.marked != nil
	r.mu.Unlock()
	if pending {
		return model.CallInfo{}, ErrContextOverflowPending
	}
	info, ok := model.CallInfoFrom(ctx)
	if !ok || info.Attempt < 1 || info.Seq < 1 || info.RunID == "" || info.RootRunID == "" || info.RootRunID != r.scope {
		return model.CallInfo{}, fmt.Errorf("record model call: valid CallInfo with positive activity attempt and sequence is required")
	}
	if r.viewErr != nil {
		return model.CallInfo{}, r.viewErr
	}
	return info, nil
}

func (c *recordedCall) storeResponse(ctx context.Context, response *model.Response) (*runstore.Hash, error) {
	if response != nil {
		data, err := callrecord.Encode(response)
		if err != nil {
			return nil, err
		}
		hash, err := c.recording.store.Put(ctx, c.recording.scope, data)
		if err != nil {
			return nil, fmt.Errorf("store model response: %w", err)
		}
		return &hash, nil
	}
	return nil, nil
}
