package agenttest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	runstoreinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"go.opentelemetry.io/otel/trace"
)

// Register wires application agents, tools and model clients into a fresh runtime.
type Register func(context.Context, *runtime.Runtime) error

// Option configures harness construction.
type Option func(*harnessOptions) error

// RunOption configures a runtime run. WithRunID is reserved by the harness.
type RunOption = runtime.RunOption

// Input is one turn's caller-supplied messages.
type Input []*model.Message

// Result separates the agent outcome from harness setup and observation failures.
type Result struct {
	// EventsUnverified reports that a BeforeEvent interceptor may have altered observed events.
	EventsUnverified bool
	RunID            string
	SessionID        string
	TraceID          string
	Output           *api.RunOutput
	RunErr           error
	Record           *runrecord.RunRecord
	Trajectory       *trajectory.Trajectory
	Awaits           []AnsweredAwait
}

// AnsweredAwait records an await and the typed answer delivered to the runtime.
type AnsweredAwait struct {
	Kind   hooks.EventType
	ID     string
	Event  hooks.Event
	Answer any
}

// Harness owns an isolated in-memory runtime and its test lifecycle.
// Runs may execute concurrently; Close prevents further starts and reports leaks.
type Harness struct {
	rt                *runtime.Runtime
	tb                testing.TB
	stubs             stubTable
	scripts           map[string]*Script
	models            map[string]model.Client
	gates             map[string]model.Client
	capturing         map[string]*captureState
	defaultRunOptions []RunOption
	timeout           time.Duration
	eventsUnverified  bool
	responder         Responder
	provider          trace.TracerProvider
	mu                sync.Mutex
	sessionCounter    uint64
	runCounter        uint64
	closed            bool
	runs              map[string]*harnessRun
}

type harnessOptions struct {
	defaultRunOptions []RunOption
	runtime           []runtime.RuntimeOption
	scripts           map[string]*Script
	live              []string
	timeout           time.Duration
	responder         Responder
	provider          trace.TracerProvider
	passthroughAll    bool
}

type harnessRun struct {
	handle engine.WorkflowHandle
	done   chan struct{}
	output *api.RunOutput
	err    error
	worker *awaitWorker
}

const cleanupTimeout = 5 * time.Second

// NewHarness constructs owned stores, engine and bus, then registers application
// components followed by scripted overrides. It never enables live clients itself.
func NewHarness(ctx context.Context, register Register, opts ...Option) (*Harness, error) {
	return newHarness(ctx, register, nil, opts...)
}

// New constructs a harness using the test context and reports cleanup leaks to t.
func New(t testing.TB, register Register, opts ...Option) *Harness {
	t.Helper()
	h, err := NewHarness(t.Context(), register, opts...)
	if err != nil {
		if errors.Is(err, ErrLiveModelDisabled) {
			t.Skipf("%v", err)
		} else {
			t.Fatal(err)
		}
		return nil
	}
	h.tb = t
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}

// WithRuntimeOptions passes application runtime settings through, rejecting the
// engine, stores, hooks and tracer owned by the harness.
func WithRuntimeOptions(opts ...runtime.RuntimeOption) Option {
	return func(options *harnessOptions) error {
		options.runtime = append(options.runtime, opts...)
		return nil
	}
}

// WithDefaultRunOptions applies opts to every Run, RunSession and
// CaptureFirstRequest invocation before caller options. WithRunID remains reserved.
func WithDefaultRunOptions(opts ...RunOption) Option {
	owned := slices.Clone(opts)
	return func(options *harnessOptions) error {
		options.defaultRunOptions = append(options.defaultRunOptions, owned...)
		return nil
	}
}

// ScriptedModel builds a deterministic model override during construction.
func ScriptedModel(id string, turns ...Turn) Option {
	return func(options *harnessOptions) error {
		script, err := NewScript(turns...)
		if err != nil {
			return fmt.Errorf("agenttest: model %q: %w", id, err)
		}
		return ScriptedModelFrom(id, script)(options)
	}
}

// ScriptedModelFrom installs a script, including its AnyOrder and context settings.
func ScriptedModelFrom(id string, script *Script) Option {
	return func(options *harnessOptions) error {
		if script == nil {
			return fmt.Errorf("agenttest: model %q: nil script", id)
		}
		options.scripts[id] = script
		return nil
	}
}

// WithRunTimeout sets the wall-clock limit, including external waits, for each run.
func WithRunTimeout(timeout time.Duration) Option {
	return func(options *harnessOptions) error {
		if timeout <= 0 {
			return errors.New("agenttest: run timeout must be positive")
		}
		options.timeout = timeout
		return nil
	}
}

// User constructs one user text message.
func User(text string) Input {
	return Input{&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: text}}}}
}

// Runtime returns the sealed harness-owned runtime for application inspection.
// Register models, agents and toolsets in the construction callback.
func (h *Harness) Runtime() *runtime.Runtime {
	return h.rt
}

// Model returns the pre-gate registered client for direct Decide calls, or nil
// when the ID is unknown.
func (h *Harness) Model(id string) model.Client {
	return h.models[id]
}

// Script returns a scripted override, or nil when the ID has no script.
func (h *Harness) Script(id string) *Script {
	return h.scripts[id]
}

// Run executes one turn in a fresh session. Failed agent outcomes populate
// RunErr. Stub dispatch failures return a populated Result and a joined harness
// error after completion; setup, timeout and reconstruction also use the error return.
func (h *Harness) Run(ctx context.Context, id agent.Ident, input Input, opts ...RunOption) (*Result, error) {
	sessionID, err := h.newSession(ctx)
	if err != nil {
		return nil, err
	}
	return h.run(ctx, id, sessionID, input, opts...)
}

// Close reports every unconsumed script/error and open run, cancelling open runs.
func (h *Harness) Close() error {
	h.mu.Lock()
	h.closed = true
	runs := make(map[string]*harnessRun, len(h.runs))
	for id, run := range h.runs {
		runs[id] = run
	}
	h.mu.Unlock()
	var diagnostics []error
	for _, id := range sortedScriptIDs(h.scripts) {
		if err := h.scripts[id].Err(); err != nil {
			diagnostics = append(diagnostics, fmt.Errorf("agenttest: model %q: %w", id, err))
		}
	}
	ids := make([]string, 0, len(runs))
	for id := range runs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		run := runs[id]
		alive := run.worker.alive()
		diagnostics = append(diagnostics, run.worker.leaks(id)...)
		select {
		case <-run.done:
		default:
			diagnostics = append(diagnostics, fmt.Errorf("agenttest: run %s: Wait has not returned", id))
			if err := stopRun(context.Background(), run); err != nil {
				diagnostics = append(diagnostics, fmt.Errorf("agenttest: run %s cleanup: %w", id, err))
			}
		}
		closeErr := run.worker.close(context.Background())
		if closeErr != nil {
			diagnostics = append(diagnostics, fmt.Errorf("agenttest: run %s cleanup: %w", id, closeErr))
		}
		if alive && !errors.Is(closeErr, errResponderAlive) {
			diagnostics = append(diagnostics, fmt.Errorf("agenttest: run %s: %w", id, errResponderAlive))
		}
	}
	diagnostics = append(diagnostics, h.stubDiagnostics(true))
	return errors.Join(diagnostics...)
}

// newHarness exposes the gate-to-seal boundary to deterministic package tests.
func newHarness(ctx context.Context, register Register, beforeSeal func(*runtime.Runtime), opts ...Option) (*Harness, error) {
	options, err := validatedHarnessOptions(opts)
	if err != nil {
		return nil, err
	}
	probe, err := harnessRuntimeOptions(&options)
	if err != nil {
		return nil, err
	}
	h := &Harness{
		defaultRunOptions: slices.Clone(options.defaultRunOptions), scripts: options.scripts, timeout: options.timeout, responder: options.responder, provider: options.provider,
		runs: make(map[string]*harnessRun), models: make(map[string]model.Client), capturing: make(map[string]*captureState),
		stubs: stubTable{
			entries: make(map[tools.Ident]stubEntry), counts: make(map[tools.Ident]int),
			passthrough: make(map[tools.Ident]bool), passthroughAll: options.passthroughAll,
		},
	}
	probe.Interceptors = append([]runtime.Interceptor{runtime.ToolInterceptorFuncs{BeforeToolFunc: h.beforeStub}}, probe.Interceptors...)
	rt := runtime.New(func(options *runtime.Options) {
		*options = probe
	})
	if err := register(ctx, rt); err != nil {
		return nil, fmt.Errorf("agenttest: register: %w", err)
	}
	h.rt = rt
	if err := h.registerModels(options); err != nil {
		return nil, err
	}
	if beforeSeal != nil {
		beforeSeal(rt)
	}
	if err := rt.Seal(ctx); err != nil {
		return nil, fmt.Errorf("agenttest: seal registration after installing capture gates: %w", err)
	}
	if err := h.verifyGates(); err != nil {
		return nil, err
	}
	h.eventsUnverified = rt.HasEventInterceptors()
	return h, nil
}

// harnessRuntimeOptions validates application options and creates harness-owned infrastructure.
func harnessRuntimeOptions(options *harnessOptions) (runtime.Options, error) {
	var probe runtime.Options
	for _, option := range options.runtime {
		option(&probe)
	}
	if err := validateOwnedOptions(probe); err != nil {
		return runtime.Options{}, err
	}
	probe.Engine = engineinmem.New()
	probe.RunStore = runstoreinmem.New()
	probe.RunEventStore = runloginmem.New()
	probe.SessionStore = sessioninmem.New()
	probe.Hooks = hooks.NewBus()
	probe.Tracer = configureTracer(options)
	return probe, nil
}

// validatedHarnessOptions applies declarations and rejects live/script conflicts
// before application registration can load credentials.
func validatedHarnessOptions(opts []Option) (harnessOptions, error) {
	options := harnessOptions{scripts: make(map[string]*Script), timeout: 30 * time.Second}
	for _, option := range opts {
		if err := option(&options); err != nil {
			return options, err
		}
	}
	for _, id := range options.live {
		if _, ok := options.scripts[id]; ok {
			return options, fmt.Errorf("agenttest: model %q cannot be both live and scripted", id)
		}
	}
	return options, nil
}

func validateOwnedOptions(options runtime.Options) error {
	for _, field := range []struct {
		name string
		set  bool
	}{
		{"Engine", options.Engine != nil}, {"RunStore", options.RunStore != nil},
		{"RunEventStore", options.RunEventStore != nil}, {"SessionStore", options.SessionStore != nil},
		{"Hooks", options.Hooks != nil}, {"Tracer", options.Tracer != nil},
	} {
		if field.set {
			return fmt.Errorf("agenttest: WithRuntimeOptions cannot override owned %s", field.name)
		}
	}
	return nil
}

func sortedScriptIDs(scripts map[string]*Script) []string {
	ids := make([]string, 0, len(scripts))
	for id := range scripts {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// stopRun cancels and drains the actual Wait, rather than merely timing it out.
func stopRun(ctx context.Context, run *harnessRun) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	cancelErr := run.handle.Cancel(cleanup)
	select {
	case <-run.done:
		return cancelErr
	case <-cleanup.Done():
		return errors.Join(cancelErr, cleanup.Err())
	}
}

func (h *Harness) newSession(ctx context.Context) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", errors.New("agenttest: harness is closed")
	}
	h.sessionCounter++
	id := fmt.Sprintf("agenttest-s%d", h.sessionCounter)
	if _, err := h.rt.CreateSession(ctx, id); err != nil {
		return "", fmt.Errorf("agenttest: session %s: %w", id, err)
	}
	return id, nil
}

func (h *Harness) run(ctx context.Context, id agent.Ident, sessionID string, input Input, opts ...RunOption) (*Result, error) {
	return h.runCapture(ctx, id, sessionID, input, nil, opts...)
}

func (h *Harness) runCapture(ctx context.Context, id agent.Ident, sessionID string, input Input, capture *captureState, opts ...RunOption) (*Result, error) {
	if err := h.stubDiagnostics(false); err != nil {
		return nil, err
	}
	var err error
	opts, err = h.prepareRunOptions(opts)
	if err != nil {
		return nil, err
	}
	client, err := h.rt.Client(id)
	if err != nil {
		return nil, fmt.Errorf("agenttest: client: %w", err)
	}
	ctx, traceID, end := h.startTrace(ctx)
	defer end()
	runCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	runID, run, err := h.startRun(runCtx, client, sessionID, input, opts, capture)
	if err != nil {
		return nil, err
	}
	defer func() {
		h.mu.Lock()
		delete(h.capturing, runID)
		h.mu.Unlock()
	}()
	if err := h.waitRun(runCtx, runID, run); err != nil {
		return nil, err
	}

	record, err := runrecord.Read(runCtx, h.rt.RunStore, h.rt.RunEventStore, runID, runID)
	if err != nil {
		return nil, fmt.Errorf("agenttest: read %s: %w", runID, err)
	}
	tr, err := trajectory.Build(record, runID, trajectory.Options{Output: run.output, EventsUnverified: h.eventsUnverified})
	if err != nil {
		return nil, fmt.Errorf("agenttest: build %s: %w", runID, err)
	}
	tr.TraceID = traceID
	return &Result{EventsUnverified: h.eventsUnverified, RunID: runID, SessionID: sessionID, TraceID: traceID, Output: run.output, RunErr: run.err, Record: record, Trajectory: tr, Awaits: run.worker.answered()}, h.stubRunErrors(runID)
}

// waitRun separates agent completion from responder and timeout failures, and
// closes every per-run observer before exposing evidence to result assembly.
func (h *Harness) waitRun(ctx context.Context, runID string, run *harnessRun) error {
	select {
	case <-run.done:
	case err := <-run.worker.failures:
		return errors.Join(err, stopRun(ctx, run), run.worker.close(ctx))
	case <-ctx.Done():
		diagnostic := fmt.Errorf("agenttest: run %s timeout or cancellation: %w", runID, ctx.Err())
		if h.eventsUnverified {
			diagnostic = fmt.Errorf("%w; a BeforeEvent interceptor may have dropped an await", diagnostic)
		}
		return errors.Join(diagnostic, stopRun(ctx, run), run.worker.close(ctx))
	}
	if err := run.worker.close(ctx); err != nil {
		return err
	}
	select {
	case err := <-run.worker.failures:
		return err
	default:
	}
	return nil
}

func (h *Harness) startRun(ctx context.Context, client runtime.AgentClient, sessionID string, input Input, opts []RunOption, capture *captureState) (string, *harnessRun, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", nil, errors.New("agenttest: harness is closed")
	}
	h.runCounter++
	runID := fmt.Sprintf("agenttest-r%d", h.runCounter)
	options := append(slices.Clone(opts), runtime.WithRunID(runID))
	worker, err := h.subscribeAwaits(ctx, runID)
	if err != nil {
		return "", nil, err
	}
	if capture != nil {
		h.capturing[runID] = capture
	}
	handle, err := client.Start(ctx, sessionID, input, options...)
	if err != nil {
		delete(h.capturing, runID)
		return "", nil, errors.Join(fmt.Errorf("agenttest: start %s: %w", runID, err), worker.close(ctx))
	}
	run := &harnessRun{handle: handle, done: make(chan struct{}), worker: worker}
	h.runs[runID] = run
	go func() {
		run.output, run.err = handle.Wait(context.WithoutCancel(ctx))
		close(run.done)
	}()
	return runID, run, nil
}

// registerModels checks explicit live declarations before scripted overrides and
// installs the capture layer once, before construction closes registration.
func (h *Harness) registerModels(options harnessOptions) error {
	for _, id := range options.live {
		if _, ok := h.rt.ModelClient(id); !ok {
			return fmt.Errorf("agenttest: application registered no client for %q", id)
		}
	}
	for _, id := range sortedScriptIDs(options.scripts) {
		if err := h.rt.RegisterModel(id, options.scripts[id].Client()); err != nil {
			return fmt.Errorf("agenttest: model %q: %w", id, err)
		}
	}
	return h.installGates()
}

// prepareRunOptions applies defaults before callers and rejects every RunID override.
func (h *Harness) prepareRunOptions(opts []RunOption) ([]RunOption, error) {
	opts = append(slices.Clone(h.defaultRunOptions), opts...)
	// Two distinct initial values detect even an override equal to a sentinel.
	first := runtime.RunInput{RunID: "agenttest-reserved"}
	second := runtime.RunInput{}
	for _, option := range opts {
		option(&first)
		option(&second)
		if first.RunID != "agenttest-reserved" || second.RunID != "" {
			return nil, errors.New("agenttest: RunID is reserved")
		}
	}
	return opts, nil
}
