package selvedge

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
)

// Options supplies the owned, unstarted worker App and optional caller-owned client.
type Options struct {
	// App is required in worker mode and must not already be started.
	App *sv.App
	// Client is optional in worker mode and required in client mode. A supplied
	// client must already be started; its caller remains responsible for shutdown.
	Client *sv.App
}

// Engine implements durable workflows using Selvedge. Register before sealing;
// Close releases only the worker App, never a separately supplied Client.
type Engine struct {
	app, client                        *sv.App
	mu                                 sync.Mutex
	registrationClosed, active, closed bool
	pending                            *activation
	cancel                             context.CancelFunc
	cleanupErr                         error
	workflows                          map[string]struct{}
	activities                         activityRegistry
	startWorkflow                      func(context.Context, *sv.App, string, any, ...sv.StartOption) (string, error)
}

type activation struct {
	done      chan struct{}
	cancel    context.CancelFunc
	abandoned bool
	accepted  bool
	err       error
}

// NewWorker constructs an engine that owns opts.App's lifecycle.
func NewWorker(opts Options) (*Engine, error) {
	if opts.App == nil {
		return nil, errors.New("selvedge: worker App is required")
	}
	if opts.Client == nil {
		opts.Client = opts.App
	}
	return &Engine{app: opts.App, client: opts.Client, workflows: make(map[string]struct{}), activities: newActivityRegistry(), startWorkflow: sv.StartWorkflowByName}, nil
}

// NewClient constructs an engine using a started, caller-owned Client.
func NewClient(opts Options) (*Engine, error) {
	if opts.Client == nil || opts.App != nil {
		return nil, errors.New("selvedge: client mode requires Client and no App")
	}
	return &Engine{client: opts.Client, startWorkflow: sv.StartWorkflowByName}, nil
}

// Close stops the owned worker, waiting for pending startup and cleanup.
func (e *Engine) Close() error {
	e.mu.Lock()
	e.closed = true
	if e.pending != nil {
		e.pending.abandoned = true
		e.pending.cancel()
	} else if e.active {
		e.cancel()
		e.active = false
		pending := &activation{done: make(chan struct{}), cancel: func() {}}
		e.pending = pending
		go e.shutdown(pending)
	}
	pending := e.pending
	e.mu.Unlock()
	if pending != nil {
		<-pending.done
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cleanupErr
}

// SealRegistration closes registration and waits for worker activation. A
// successful worker has an independent lifetime; expired activation is cleaned
// up before another attempt may start.
func (e *Engine) SealRegistration(ctx context.Context) error {
	if e.app == nil {
		return nil
	}
	for {
		e.mu.Lock()
		e.registrationClosed = true
		if done, err := e.activationState(ctx); done {
			e.mu.Unlock()
			return err
		}
		if pending := e.pending; pending != nil {
			e.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-pending.done:
				if err := ctx.Err(); err != nil {
					return err
				}
				continue
			}
		}
		lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
		pending := &activation{done: make(chan struct{}), cancel: cancel}
		e.pending = pending
		e.mu.Unlock()
		go e.activate(ctx, lifetime, pending)
		select {
		case <-pending.done:
			return activationResult(ctx, pending)
		case <-ctx.Done():
			return e.cancelActivation(ctx, pending)
		}
	}
}

// RegisterWorkflow registers a typed workflow before activation.
func (e *Engine) RegisterWorkflow(_ context.Context, def engine.WorkflowDefinition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.registrationError(def.Name); err != nil {
		return err
	}
	if def.Handler == nil {
		return errors.New("selvedge: workflow handler is required")
	}
	if _, exists := e.workflows[def.Name]; exists {
		return fmt.Errorf("selvedge: workflow %q already registered", def.Name)
	}
	workflow := sv.DefineWorkflow(def.Name, func(ctx *sv.WorkflowContext, input *api.RunInput) (*api.RunOutput, error) {
		output, err := def.Handler(&workflowContext{engine: e, ctx: ctx}, input)
		return output, workflowError(err)
	})
	sv.RegisterWorkflow(e.app, workflow)
	e.workflows[def.Name] = struct{}{}
	return nil
}

// StartWorkflow starts by name without requiring a local definition.
func (e *Engine) StartWorkflow(ctx context.Context, req engine.WorkflowStartRequest) (engine.WorkflowHandle, error) {
	if req.ID == "" || req.Workflow == "" {
		return nil, fmt.Errorf("%w: workflow ID and name are required", engine.ErrWorkflowStartRejected)
	}
	id, err := e.startWorkflow(ctx, e.client, req.Workflow, req.Input, sv.WithInstanceID(req.ID))
	if err != nil {
		return nil, startError(err)
	}
	return &workflowHandle{id: id, client: e.client}, nil
}

func (e *Engine) registrationError(name string) error {
	if e.app == nil {
		return errors.New("selvedge: registration needs a worker-mode engine")
	}
	if e.registrationClosed || e.closed {
		return errors.New("selvedge: registration is closed")
	}
	if name == "" {
		return errors.New("selvedge: registration name is required")
	}
	return nil
}

func (e *Engine) activate(caller, lifetime context.Context, pending *activation) {
	err := e.app.Start(lifetime)
	e.mu.Lock()
	if err == nil && !pending.abandoned && !e.closed && caller.Err() == nil {
		e.active = true
		pending.accepted = true
		e.cancel = pending.cancel
		e.pending = nil
		close(pending.done)
		e.mu.Unlock()
		return
	}
	pending.cancel()
	pending.err = err
	if caller.Err() != nil {
		pending.err = caller.Err()
	} else if pending.err == nil {
		pending.err = errors.New("selvedge: activation canceled by Close")
	}
	if err != nil {
		e.pending = nil
		close(pending.done)
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	e.shutdown(pending)
}

func (e *Engine) shutdown(pending *activation) {
	err := e.app.Shutdown(context.Background())
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		// Selvedge cannot wait again after an incomplete Shutdown. Keep ownership
		// failed closed instead of starting on top of unobserved background cleanup.
		e.cleanupErr = fmt.Errorf("selvedge: worker cleanup incomplete: %w", err)
		pending.err = e.cleanupErr
	}
	e.pending = nil
	close(pending.done)
}

func (e *Engine) cancelActivation(ctx context.Context, pending *activation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pending.accepted {
		return nil
	}
	pending.abandoned = true
	pending.cancel()
	return ctx.Err()
}

// activationState is called with mu held and distinguishes a completed seal
// from an activation request whose caller must still be live.
func (e *Engine) activationState(ctx context.Context) (bool, error) {
	if e.cleanupErr != nil {
		return true, e.cleanupErr
	}
	if e.closed {
		return true, errors.New("selvedge: engine closed")
	}
	if e.active {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	return false, nil
}

// activationResult reads a completed activation after its done channel closes.
func activationResult(ctx context.Context, pending *activation) error {
	if !pending.accepted {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return pending.err
}
