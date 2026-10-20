// Package inmem provides an in-memory workflow engine implementation for
// tests and local development.
//
// The in-memory engine is intentionally minimal:
// - It runs workflow handlers in-process in goroutines (no durability).
// - It does not provide Temporal-like determinism or replay semantics.
// - Activity and workflow timeouts are best-effort and use the standard library.
//
// This engine is useful for unit tests that want to exercise runtime logic
// without standing up an external workflow backend.
package inmem

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
)

type (
	// eng implements engine.Engine with an in-process goroutine runner.
	eng struct {
		engine.AgentActivityRegistrations
		activities map[string]activityDef
		mu         sync.RWMutex

		workflows map[string]engine.WorkflowDefinition

		// statuses tracks workflow status by run ID (inmem uses workflow ID as run ID).
		statuses map[string]engine.RunStatus
		handles  map[string]*handle

		// observeActivity records effective dispatch options for conformance tests.
		// Set before starting workflows; the observer must be concurrency-safe.
		observeActivity func(workflowID, name string, options engine.ActivityOptions)
	}

	// wfCtx adapts context.Context plus in-memory signal channels into engine.WorkflowContext.
	wfCtx struct {
		engine.AgentWorkflowActivities
		ctx     context.Context
		id      string
		runID   string
		eng     *eng
		queries map[string]any

		pauseCh       chan *api.PauseRequest
		resumeCh      chan *api.ResumeRequest
		clarifyCh     chan *api.ClarificationAnswer
		toolResultsCh chan *api.ToolResultsSet
		confirmCh     chan *api.ConfirmationDecision
		typedInputCh  chan *api.TypedInputAnswer
	}

	// handle is the in-memory implementation of engine.WorkflowHandle.
	handle struct {
		mu     sync.Mutex
		done   chan struct{}
		err    error
		result *api.RunOutput
		wfCtx  *wfCtx
		cancel context.CancelFunc
	}

	// childHandle adapts an in-memory WorkflowHandle to engine.ChildWorkflowHandle.
	childHandle struct {
		h engine.WorkflowHandle
	}

	// future is a simple typed Future implementation backed by a channel.
	future[T any] struct {
		ready  chan struct{}
		result T
		err    error
	}

	// receiver is a typed in-memory signal receiver.
	receiver[T any] struct {
		ch chan T
	}
)

var (
	_ engine.Engine              = (*eng)(nil)
	_ engine.WorkflowHandle      = (*handle)(nil)
	_ engine.WorkflowContext     = (*wfCtx)(nil)
	_ engine.ChildWorkflowHandle = (*childHandle)(nil)
)

// New returns a new in-memory workflow engine.
//
// This engine is intended for tests and local development only. It does not
// provide durability, determinism, or replay safety.
func New() engine.Engine {
	e := &eng{
		statuses: make(map[string]engine.RunStatus),
	}
	e.AgentActivityRegistrations.ActivityRegistrar = e
	return e
}

func (e *eng) RegisterWorkflow(_ context.Context, def engine.WorkflowDefinition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.workflows == nil {
		e.workflows = make(map[string]engine.WorkflowDefinition)
	}
	if _, dup := e.workflows[def.Name]; dup {
		return fmt.Errorf("workflow %q already registered", def.Name)
	}
	if def.Handler == nil || def.Name == "" {
		return errors.New("invalid workflow definition")
	}
	e.workflows[def.Name] = def
	return nil
}

func (e *eng) StartWorkflow(ctx context.Context, req engine.WorkflowStartRequest) (engine.WorkflowHandle, error) {
	return e.startWorkflow(ctx, context.WithoutCancel(ctx), req)
}

// startWorkflow separates acceptance cancellation from the accepted run lifetime.
func (e *eng) startWorkflow(acceptanceCtx, workflowParent context.Context, req engine.WorkflowStartRequest) (engine.WorkflowHandle, error) {
	if err := acceptanceCtx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", engine.ErrWorkflowStartRejected, err)
	}
	def, err := e.lookupWorkflow(req.Workflow)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", engine.ErrWorkflowStartRejected, err)
	}
	if err := validateWorkflowStartRequest(req); err != nil {
		return nil, fmt.Errorf("%w: %w", engine.ErrWorkflowStartRejected, err)
	}
	runCtx, cancel := workflowRunContext(workflowParent, req.RunTimeout)
	wctx := e.newWorkflowContext(runCtx, req.ID)
	h := &handle{done: make(chan struct{}), wfCtx: wctx, cancel: cancel}
	if err := e.reserveWorkflowRun(acceptanceCtx, req.ID, h); err != nil {
		cancel()
		return nil, err
	}
	go func() {
		defer close(h.done)
		defer cancel()
		res, err := def.Handler(wctx, req.Input)
		if runErr := runCtx.Err(); runErr != nil {
			res = nil
			err = errors.Join(runErr, err)
		}
		h.mu.Lock()
		h.result = res
		h.err = normalizeError(err)
		h.mu.Unlock()
		// Update status based on completion.
		e.mu.Lock()
		if err != nil {
			if cancellation.Only(err) {
				e.statuses[req.ID] = engine.RunStatusCanceled
			} else {
				e.statuses[req.ID] = engine.RunStatusFailed
			}
		} else {
			e.statuses[req.ID] = engine.RunStatusCompleted
		}
		e.mu.Unlock()
	}()

	return h, nil
}

// workflowRunContext applies an optional execution deadline to the run lifetime.
func workflowRunContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func (e *eng) lookupWorkflow(name string) (engine.WorkflowDefinition, error) {
	e.mu.RLock()
	def, ok := e.workflows[name]
	e.mu.RUnlock()
	if !ok {
		return engine.WorkflowDefinition{}, fmt.Errorf("workflow %q not registered", name)
	}
	return def, nil
}

func validateWorkflowStartRequest(req engine.WorkflowStartRequest) error {
	if req.ID == "" {
		return errors.New("workflow id is required")
	}
	return nil
}

func (e *eng) newWorkflowContext(ctx context.Context, id string) *wfCtx {
	w := &wfCtx{
		ctx:           ctx,
		id:            id,
		runID:         id,
		eng:           e,
		pauseCh:       make(chan *api.PauseRequest, 1),
		resumeCh:      make(chan *api.ResumeRequest, 1),
		clarifyCh:     make(chan *api.ClarificationAnswer, 1),
		toolResultsCh: make(chan *api.ToolResultsSet, 1),
		confirmCh:     make(chan *api.ConfirmationDecision, 1),
		typedInputCh:  make(chan *api.TypedInputAnswer, 1),
	}
	w.AgentWorkflowActivities.ActivityExecutor = w
	return w
}

func (e *eng) reserveWorkflowRun(acceptanceCtx context.Context, id string, h *handle) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := acceptanceCtx.Err(); err != nil {
		return fmt.Errorf("%w: %w", engine.ErrWorkflowStartRejected, err)
	}
	if e.statuses == nil {
		e.statuses = make(map[string]engine.RunStatus)
	}
	if _, exists := e.statuses[id]; exists {
		return fmt.Errorf("workflow ID %q already exists: %w", id, engine.ErrWorkflowAlreadyStarted)
	}
	if e.handles == nil {
		e.handles = make(map[string]*handle)
	}
	e.statuses[id] = engine.RunStatusRunning
	e.handles[id] = h
	return nil
}

// QueryRunStatus returns the current lifecycle status for a workflow execution.
func (e *eng) QueryRunStatus(_ context.Context, workflowID string) (engine.RunStatus, error) {
	if workflowID == "" {
		return "", fmt.Errorf("workflow id is required")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	status, ok := e.statuses[workflowID]
	if !ok {
		return "", engine.ErrWorkflowNotFound
	}
	return status, nil
}

func (e *eng) CancelByID(ctx context.Context, workflowID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if workflowID == "" {
		return errors.New("workflow id is required")
	}
	e.mu.RLock()
	h, ok := e.handles[workflowID]
	e.mu.RUnlock()
	if !ok {
		return engine.ErrWorkflowNotFound
	}
	return h.Cancel(ctx)
}

func (h *handle) Wait(ctx context.Context) (*api.RunOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.done:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.result, h.err
	}
}

func (h *handle) Signal(ctx context.Context, name string, payload any) error {
	switch name {
	case api.SignalPause:
		return h.signalPause(ctx, name, payload)
	case api.SignalResume:
		return h.signalResume(ctx, name, payload)
	case api.SignalProvideClarification:
		return h.signalClarification(ctx, name, payload)
	case api.SignalProvideToolResults:
		return h.signalToolResults(ctx, name, payload)
	case api.SignalProvideConfirmation:
		return h.signalConfirmation(ctx, name, payload)
	case api.SignalProvideTypedInput:
		return h.signalTypedInput(ctx, name, payload)
	default:
		return fmt.Errorf("unknown signal %q", name)
	}
}

func (h *handle) signalPause(ctx context.Context, name string, payload any) error {
	req, ok := payload.(*api.PauseRequest)
	if !ok {
		return fmt.Errorf("signal %q expects api.PauseRequest, got %T", name, payload)
	}
	return sendSignal(ctx, h.done, h.wfCtx.pauseCh, req)
}

func (h *handle) signalResume(ctx context.Context, name string, payload any) error {
	req, ok := payload.(*api.ResumeRequest)
	if !ok {
		return fmt.Errorf("signal %q expects api.ResumeRequest, got %T", name, payload)
	}
	return sendSignal(ctx, h.done, h.wfCtx.resumeCh, req)
}

func (h *handle) signalClarification(ctx context.Context, name string, payload any) error {
	req, ok := payload.(*api.ClarificationAnswer)
	if !ok {
		return fmt.Errorf("signal %q expects api.ClarificationAnswer, got %T", name, payload)
	}
	return sendSignal(ctx, h.done, h.wfCtx.clarifyCh, req)
}

func (h *handle) signalToolResults(ctx context.Context, name string, payload any) error {
	req, ok := payload.(*api.ToolResultsSet)
	if !ok {
		return fmt.Errorf("signal %q expects api.ToolResultsSet, got %T", name, payload)
	}
	return sendSignal(ctx, h.done, h.wfCtx.toolResultsCh, req)
}

func (h *handle) signalConfirmation(ctx context.Context, name string, payload any) error {
	req, ok := payload.(*api.ConfirmationDecision)
	if !ok {
		return fmt.Errorf("signal %q expects api.ConfirmationDecision, got %T", name, payload)
	}
	return sendSignal(ctx, h.done, h.wfCtx.confirmCh, req)
}

func (h *handle) signalTypedInput(ctx context.Context, name string, payload any) error {
	req, ok := payload.(*api.TypedInputAnswer)
	if !ok {
		return fmt.Errorf("signal %q expects api.TypedInputAnswer, got %T", name, payload)
	}
	return sendSignal(ctx, h.done, h.wfCtx.typedInputCh, req)
}

func (h *handle) Cancel(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-h.done:
		return engine.ErrWorkflowCompleted
	default:
		h.cancel()
		return nil
	}
}

func (c *childHandle) Get(ctx context.Context) (*api.RunOutput, error) {
	return c.h.Wait(ctx)
}

func (c *childHandle) IsReady() bool {
	if h, ok := c.h.(*handle); ok {
		select {
		case <-h.done:
			return true
		default:
			return false
		}
	}
	return false
}

func (c *childHandle) Cancel(ctx context.Context) error {
	return c.h.Cancel(ctx)
}

func (c *childHandle) RunID() string {
	if h, ok := c.h.(*handle); ok {
		return h.wfCtx.runID
	}
	return ""
}

func (w *wfCtx) Context() context.Context {
	return engine.WithWorkflowContext(w.ctx, w)
}

// SetQueryHandler retains read-only query handlers for this execution.
func (w *wfCtx) SetQueryHandler(name string, handler any) error {
	w.eng.mu.Lock()
	defer w.eng.mu.Unlock()
	if w.queries == nil {
		w.queries = make(map[string]any)
	}
	w.queries[name] = handler
	return nil
}

func (w *wfCtx) WorkflowID() string {
	return w.id
}

func (w *wfCtx) RunID() string {
	return w.runID
}

func (w *wfCtx) StartChildWorkflow(ctx context.Context, req engine.ChildWorkflowRequest) (engine.ChildWorkflowHandle, error) {
	h, err := w.eng.startWorkflow(ctx, ctx, engine.WorkflowStartRequest{
		ID:          req.ID,
		Workflow:    req.Workflow,
		TaskQueue:   req.TaskQueue,
		Input:       req.Input,
		RunTimeout:  req.RunTimeout,
		RetryPolicy: req.RetryPolicy,
	})
	if err != nil {
		return nil, err
	}
	return &childHandle{h: h}, nil
}

func (w *wfCtx) Detached() engine.WorkflowContext {
	cctx := context.WithoutCancel(w.ctx)
	sub := *w
	sub.ctx = cctx
	sub.AgentWorkflowActivities.ActivityExecutor = &sub
	return &sub
}

func (w *wfCtx) WithCancel() (engine.WorkflowContext, func()) {
	cctx, cancel := context.WithCancel(w.ctx)
	sub := *w
	sub.ctx = cctx
	sub.AgentWorkflowActivities.ActivityExecutor = &sub
	return &sub, cancel
}

func (w *wfCtx) Now() time.Time {
	return time.Now()
}

func (w *wfCtx) NewTimer(ctx context.Context, d time.Duration) (engine.Future[time.Time], error) {
	now := time.Now()
	if d <= 0 {
		fut := &future[time.Time]{ready: make(chan struct{}), result: now}
		close(fut.ready)
		return fut, nil
	}
	fireAt := now.Add(d)
	fut := &future[time.Time]{ready: make(chan struct{})}
	go func() {
		defer close(fut.ready)
		select {
		case <-ctx.Done():
			fut.err = normalizeError(ctx.Err())
		case <-time.After(d):
			fut.result = fireAt
		}
	}()
	return fut, nil
}

func (w *wfCtx) Await(ctx context.Context, condition func() bool) error {
	if condition == nil {
		return errors.New("await condition is required")
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *wfCtx) PauseRequests() engine.Receiver[*api.PauseRequest] {
	return receiver[*api.PauseRequest]{ch: w.pauseCh}
}

func (w *wfCtx) ResumeRequests() engine.Receiver[*api.ResumeRequest] {
	return receiver[*api.ResumeRequest]{ch: w.resumeCh}
}

func (w *wfCtx) ClarificationAnswers() engine.Receiver[*api.ClarificationAnswer] {
	return receiver[*api.ClarificationAnswer]{ch: w.clarifyCh}
}

func (w *wfCtx) ExternalToolResults() engine.Receiver[*api.ToolResultsSet] {
	return receiver[*api.ToolResultsSet]{ch: w.toolResultsCh}
}

func (w *wfCtx) ConfirmationDecisions() engine.Receiver[*api.ConfirmationDecision] {
	return receiver[*api.ConfirmationDecision]{ch: w.confirmCh}
}

func (w *wfCtx) TypedInputAnswers() engine.Receiver[*api.TypedInputAnswer] {
	return receiver[*api.TypedInputAnswer]{ch: w.typedInputCh}
}

// Receive blocks until a signal value is delivered and returns it.
func (r receiver[T]) Receive(ctx context.Context) (T, error) {
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case val := <-r.ch:
		return val, nil
	}
}

// ReceiveWithTimeout blocks until a signal value is delivered or the timeout
// elapses and returns context.DeadlineExceeded.
func (r receiver[T]) ReceiveWithTimeout(ctx context.Context, timeout time.Duration) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	if timeout <= 0 {
		var zero T
		return zero, context.DeadlineExceeded
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-timer.C:
		var zero T
		return zero, context.DeadlineExceeded
	case val := <-r.ch:
		return val, nil
	}
}

// ReceiveAsync attempts to receive a signal value without blocking.
func (r receiver[T]) ReceiveAsync() (T, bool) {
	select {
	case val := <-r.ch:
		return val, true
	default:
		var zero T
		return zero, false
	}
}

// Pending returns the buffered signal count without consuming signals.
func (r receiver[T]) Pending() int {
	return len(r.ch)
}

func (f *future[T]) Get(ctx context.Context) (T, error) {
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-f.ready:
		return f.result, f.err
	}
}

func (f *future[T]) IsReady() bool {
	select {
	case <-f.ready:
		return true
	default:
		return false
	}
}

func sendSignal[T any](ctx context.Context, done <-chan struct{}, ch chan<- T, payload T) error {
	select {
	case <-done:
		return engine.ErrWorkflowCompleted
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return engine.ErrWorkflowCompleted
	case ch <- payload:
		return nil
	}
}

// withOptionalTimeout builds an activity context. In-memory activities execute
// once, so their attempt is always 1, independent of the parent context.
func withOptionalTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	parent = engine.WithActivityAttempt(parent, 1)
	if timeout <= 0 {
		return parent, func() {
		}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel
}

// QueryRootRunID returns the immutable scope registered by the workflow.
func (e *eng) QueryRootRunID(ctx context.Context, workflowID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if workflowID == "" {
		return "", errors.New("workflow id is required")
	}
	e.mu.RLock()
	h, ok := e.handles[workflowID]
	if !ok {
		e.mu.RUnlock()
		return "", engine.ErrWorkflowNotFound
	}
	handler := h.wfCtx.queries[api.QueryRootRunID]
	e.mu.RUnlock()
	query, ok := handler.(func() (string, error))
	if !ok {
		return "", fmt.Errorf("workflow %q: root_run_id query not registered", workflowID)
	}
	return query()
}

// SignalByID delivers a signal without requiring the caller's runtime to retain
// a handle, including when the target is an agent child workflow.
func (e *eng) SignalByID(ctx context.Context, workflowID, runID, name string, payload any) error {
	e.mu.RLock()
	h, ok := e.handles[workflowID]
	e.mu.RUnlock()
	if !ok {
		return engine.ErrWorkflowNotFound
	}
	if runID != "" && runID != h.wfCtx.runID {
		return engine.ErrWorkflowNotFound
	}
	return h.Signal(ctx, name, payload)
}
