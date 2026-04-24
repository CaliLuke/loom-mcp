package runtime

import (
	"errors"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/reminder"
	"github.com/CaliLuke/loom-mcp/runtime/agent/runlog"
	runloginmem "github.com/CaliLuke/loom-mcp/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

const (
	// Opinionated defaults applied when activity timeouts are unspecified.
	defaultPlanActivityTimeout        = 2 * time.Minute
	defaultResumeActivityTimeout      = 2 * time.Minute
	defaultExecuteToolActivityTimeout = 2 * time.Minute
	defaultHookActivityTimeout        = 15 * time.Second
)

var (
	// Typed error sentinels for common invalid states.
	ErrAgentNotFound       = errors.New("agent not found")
	ErrEngineNotConfigured = errors.New("runtime engine not configured")
	ErrInvalidConfig       = errors.New("invalid configuration")
	ErrMissingSessionID    = errors.New("session id is required")
	ErrSessionNotAllowed   = errors.New("session id is not allowed")
	ErrWorkflowStartFailed = errors.New("workflow start failed")
	ErrRegistrationClosed  = errors.New("registration closed after first run")
)

// New constructs a Runtime using functional options.
func New(opts ...RuntimeOption) *Runtime {
	var o Options
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return newFromOptions(o)
}

// WithEngine sets the workflow engine.
func WithEngine(e engine.Engine) RuntimeOption { return func(o *Options) { o.Engine = e } }

// WithHookActivityTimeout sets the StartToClose timeout for the hook publishing activity.
func WithHookActivityTimeout(d time.Duration) RuntimeOption {
	if d <= 0 {
		panic("runtime: hook activity timeout must be greater than zero")
	}
	return func(o *Options) { o.HookActivityTimeout = d }
}

// WithMemoryStore sets the memory store.
func WithMemoryStore(m memory.Store) RuntimeOption { return func(o *Options) { o.MemoryStore = m } }

// WithPromptStore sets the prompt override store.
func WithPromptStore(s prompt.Store) RuntimeOption { return func(o *Options) { o.PromptStore = s } }

// WithSessionStore sets the session store.
func WithSessionStore(s session.Store) RuntimeOption { return func(o *Options) { o.SessionStore = s } }

// WithRunEventStore sets the canonical run event store.
func WithRunEventStore(s runlog.Store) RuntimeOption { return func(o *Options) { o.RunEventStore = s } }

// WithPolicy sets the policy engine.
func WithPolicy(p policy.Engine) RuntimeOption { return func(o *Options) { o.Policy = p } }

// WithStream sets the stream sink.
func WithStream(s stream.Sink) RuntimeOption { return func(o *Options) { o.Stream = s } }

// WithHooks sets the event bus.
func WithHooks(b hooks.Bus) RuntimeOption { return func(o *Options) { o.Hooks = b } }

// WithLogger sets the logger.
func WithLogger(l telemetry.Logger) RuntimeOption { return func(o *Options) { o.Logger = l } }

// WithMetrics sets the metrics recorder.
func WithMetrics(m telemetry.Metrics) RuntimeOption { return func(o *Options) { o.Metrics = m } }

// WithTracer sets the tracer.
func WithTracer(t telemetry.Tracer) RuntimeOption { return func(o *Options) { o.Tracer = t } }

// WithToolConfirmation configures runtime-enforced confirmation for selected tools.
func WithToolConfirmation(cfg *ToolConfirmationConfig) RuntimeOption {
	return func(o *Options) { o.ToolConfirmation = cfg }
}

// WithHintOverrides configures per-tool call hint overrides.
func WithHintOverrides(m map[tools.Ident]HintOverrideFunc) RuntimeOption {
	return func(o *Options) { o.HintOverrides = m }
}

// WithWorker configures the worker for a specific agent.
func WithWorker(id agent.Ident, cfg WorkerConfig) RuntimeOption {
	return func(o *Options) {
		if o.Workers == nil {
			o.Workers = make(map[agent.Ident]WorkerConfig)
		}
		o.Workers[id] = cfg
	}
}

// WithQueue returns a WorkerOption that sets the queue name on a WorkerConfig.
func WithQueue(name string) WorkerOption {
	return func(c *WorkerConfig) { c.Queue = name }
}

// newFromOptions constructs a Runtime using the provided options.
func newFromOptions(opts Options) *Runtime {
	if opts.ToolConfirmation != nil {
		if err := opts.ToolConfirmation.validate(); err != nil {
			panic(err)
		}
	}
	bus := resolveRuntimeBus(opts.Hooks)
	eng := resolveRuntimeEngine(opts.Engine)
	metrics := resolveRuntimeMetrics(opts.Metrics)
	logger := resolveRuntimeLogger(opts.Logger)
	tracer := resolveRuntimeTracer(opts.Tracer)
	runEventStore := resolveRunEventStore(opts.RunEventStore)
	sessionStore := resolveSessionStore(opts.SessionStore)
	rt := &Runtime{
		Engine:              eng,
		Memory:              opts.MemoryStore,
		PromptRegistry:      prompt.NewRegistry(opts.PromptStore),
		SessionStore:        sessionStore,
		Policy:              opts.Policy,
		RunEventStore:       runEventStore,
		Bus:                 bus,
		Stream:              opts.Stream,
		hookActivityTimeout: opts.HookActivityTimeout,
		logger:              logger,
		metrics:             metrics,
		tracer:              tracer,
		agents:              make(map[agent.Ident]AgentRegistration),
		toolsets:            make(map[string]ToolsetRegistration),
		toolSpecs:           make(map[tools.Ident]tools.ToolSpec),
		toolSchemas:         make(map[string]map[string]any),
		models:              make(map[string]model.Client),
		runHandles:          make(map[string]engine.WorkflowHandle),
		agentToolSpecs:      make(map[agent.Ident][]tools.ToolSpec),
		workers:             opts.Workers,
		reminders:           reminder.NewEngine(),
		toolConfirmation:    opts.ToolConfirmation,
		hintOverrides:       opts.HintOverrides,
	}
	rt.PromptRegistry.SetObserver(rt.onPromptRendered)
	rt.installRuntimeSubscribers(bus)
	return rt
}

func resolveRuntimeBus(bus hooks.Bus) hooks.Bus {
	if bus != nil {
		return bus
	}
	return hooks.NewBus()
}

func resolveRuntimeEngine(eng engine.Engine) engine.Engine {
	if eng != nil {
		return eng
	}
	return engineinmem.New()
}

func resolveRuntimeMetrics(metrics telemetry.Metrics) telemetry.Metrics {
	if metrics != nil {
		return metrics
	}
	return telemetry.NoopMetrics{}
}

func resolveRuntimeLogger(logger telemetry.Logger) telemetry.Logger {
	if logger != nil {
		return logger
	}
	return telemetry.NoopLogger{}
}

func resolveRuntimeTracer(tracer telemetry.Tracer) telemetry.Tracer {
	if tracer != nil {
		return tracer
	}
	return telemetry.NoopTracer{}
}

func resolveRunEventStore(store runlog.Store) runlog.Store {
	if store != nil {
		return store
	}
	return runloginmem.New()
}

func resolveSessionStore(store session.Store) session.Store {
	if store != nil {
		return store
	}
	return sessioninmem.New()
}
