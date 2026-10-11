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
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
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
		store     runstore.Store
		scope     string
		ref       index.ViewRef
		view      []*model.Message
		viewErr   error
		step      int
		mu        sync.Mutex
		keys      []runstore.CallKey
		completed []runstore.CallKey
		facts     map[runstore.CallKey]contextmeasure.RequestFacts
		growth    *RequestGrowth
		streams   []*callRecordingStream
		failures  []error
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
	recording := &CallRecording{store: store, scope: scope, ref: ref, step: step, facts: make(map[runstore.CallKey]contextmeasure.RequestFacts)}
	snapshot, err := callrecord.SnapshotMessages(view)
	if err != nil {
		recording.viewErr = fmt.Errorf("snapshot recorded view: %w", err)
	} else {
		recording.view = snapshot
	}
	return recording
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
	info, ok := model.CallInfoFrom(ctx)
	if !ok || info.Attempt < 1 || info.Seq < 1 || info.RunID == "" || info.RootRunID == "" || info.RootRunID != r.scope {
		return nil, fmt.Errorf("record model call: valid CallInfo with positive activity attempt and sequence is required")
	}
	if r.viewErr != nil {
		return nil, r.viewErr
	}
	parts, sizes, err := callrecord.BuildRequest(ctx, r.store, r.scope, req, r.ref, r.view)
	if err != nil {
		return nil, fmt.Errorf("record model request: %w", err)
	}
	key := runstore.CallKey{RunID: info.RunID, Seq: info.Seq, Attempt: info.Attempt}
	facts, err := r.measureRequest(ctx, req, registered, key, info.ModelID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	started := callrecord.Started{Key: key, Activity: callrecord.ActivityPlanner, TurnID: info.TurnID, Step: r.step, Request: parts, Started: now, Model: req.Model, ModelID: info.ModelID, Class: req.ModelClass, Prompts: req.PromptRefs}
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
	if r.growth == nil && sizes.Outside > sizes.Inside {
		r.growth = &RequestGrowth{Key: key, Inside: sizes.Inside, Outside: sizes.Outside}
	}
	r.mu.Unlock()
	return &recordedCall{recording: r, record: record, started: now, facts: facts}, nil
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
	completion := callrecord.Completion{Usage: usage, Latency: time.Since(c.started), Facts: &facts}
	if response != nil {
		data, err := callrecord.Encode(response)
		if err != nil {
			return err
		}
		hash, err := c.recording.store.Put(ctx, c.recording.scope, data)
		if err != nil {
			return fmt.Errorf("store model response: %w", err)
		}
		completion.Response = &hash
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
	c.recording.mu.Lock()
	c.recording.facts[record.Key] = facts
	c.recording.mu.Unlock()
	if state == runstore.CallCompleted {
		c.recording.mu.Lock()
		c.recording.completed = append(c.recording.completed, record.Key)
		c.recording.mu.Unlock()
	}
	return nil
}

// measureRequest snapshots facts before sending, with limits from the registered
// client rather than the outer planner wrappers that drop optional capabilities.
func (r *CallRecording) measureRequest(ctx context.Context, req *model.Request, registered model.Client, key runstore.CallKey, modelID string) (contextmeasure.RequestFacts, error) {
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
		return contextmeasure.RequestFacts{}, fmt.Errorf("measure model request: %w", err)
	}
	facts.Call = key
	facts.Model = firstNonEmpty(resolved, req.Model, modelID)
	return facts, nil
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
