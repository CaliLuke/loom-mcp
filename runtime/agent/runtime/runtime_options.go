package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	bedrock "github.com/CaliLuke/loom-mcp/features/model/bedrock"
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
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// MissingFieldsAction controls behavior when a tool validation error indicates
// missing fields. It is string-backed for JSON friendliness. Empty value means
// unspecified (planner decides).
type MissingFieldsAction string

const (
	// MissingFieldsFinalize instructs the runtime to finalize immediately
	// when fields are missing.
	MissingFieldsFinalize MissingFieldsAction = "finalize"
	// MissingFieldsAwaitClarification instructs the runtime to pause and await user clarification.
	MissingFieldsAwaitClarification MissingFieldsAction = "await_clarification"
	// MissingFieldsResume instructs the runtime to continue without pausing; surface hints to the planner.
	MissingFieldsResume MissingFieldsAction = "resume"
)

const (
	// Opinionated defaults applied when activity timeouts are unspecified.
	defaultPlanActivityTimeout        = 2 * time.Minute
	defaultResumeActivityTimeout      = 2 * time.Minute
	defaultExecuteToolActivityTimeout = 2 * time.Minute
	defaultHookActivityTimeout        = 15 * time.Second
)

// defaultRetriedActivityPolicy returns the runtime's standard infrastructure
// retry policy for activities whose logical work is now replay-safe by
// contract. Planner/tool business errors still surface in typed results rather
// than escaping as activity failures.
func defaultRetriedActivityPolicy() engine.RetryPolicy {
	return engine.RetryPolicy{
		MaxAttempts:        3,
		InitialInterval:    time.Second,
		BackoffCoefficient: 2,
	}
}

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

// RunOption configures optional fields on RunInput for Run and Start. Required
// values such as SessionID are positional arguments on AgentClient methods and
// must not be set via RunOption.
type RunOption func(*RunInput)

// WithRunID sets the RunID on the constructed RunInput.
func WithRunID(id string) RunOption {
	return func(in *RunInput) { in.RunID = id }
}

// WithLabels merges the provided labels into the constructed RunInput.
func WithLabels(labels map[string]string) RunOption {
	return func(in *RunInput) { in.Labels = mergeLabels(in.Labels, labels) }
}

// WithTurnID sets the TurnID on the constructed RunInput.
func WithTurnID(id string) RunOption {
	return func(in *RunInput) { in.TurnID = id }
}

// WithMetadata merges the provided metadata into the constructed RunInput.
func WithMetadata(meta map[string]any) RunOption {
	return func(in *RunInput) {
		if len(meta) == 0 {
			return
		}
		if in.Metadata == nil {
			in.Metadata = make(map[string]any, len(meta))
		}
		for k, v := range meta {
			in.Metadata[k] = v
		}
	}
}

// WithTaskQueue sets the target task queue on WorkflowOptions for this run.
func WithTaskQueue(name string) RunOption {
	return func(in *RunInput) {
		if in.WorkflowOptions == nil {
			in.WorkflowOptions = &WorkflowOptions{}
		}
		in.WorkflowOptions.TaskQueue = name
	}
}

// WithMemo sets memo on WorkflowOptions for this run.
func WithMemo(m map[string]any) RunOption {
	return func(in *RunInput) {
		if in.WorkflowOptions == nil {
			in.WorkflowOptions = &WorkflowOptions{}
		}
		if in.WorkflowOptions.Memo == nil {
			in.WorkflowOptions.Memo = make(map[string]any, len(m))
		}
		for k, v := range m {
			in.WorkflowOptions.Memo[k] = v
		}
	}
}

// WithSearchAttributes sets search attributes on WorkflowOptions for this run.
func WithSearchAttributes(sa map[string]any) RunOption {
	return func(in *RunInput) {
		if in.WorkflowOptions == nil {
			in.WorkflowOptions = &WorkflowOptions{}
		}
		if in.WorkflowOptions.SearchAttributes == nil {
			in.WorkflowOptions.SearchAttributes = make(map[string]any, len(sa))
		}
		maps.Copy(in.WorkflowOptions.SearchAttributes, sa)
	}
}

// WithWorkflowOptions sets workflow engine options on the constructed RunInput.
func WithWorkflowOptions(o *WorkflowOptions) RunOption {
	return func(in *RunInput) { in.WorkflowOptions = o }
}

// WithTiming sets run-level timing overrides in a single structured option.
// Budget is the semantic run budget; Plan and Tools are attempt budgets. Zero-
// valued fields are ignored.
func WithTiming(t Timing) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		if t.Budget > 0 {
			in.Policy.TimeBudget = t.Budget
		}
		if t.Plan > 0 {
			in.Policy.PlanTimeout = t.Plan
		}
		if t.Tools > 0 {
			in.Policy.ToolTimeout = t.Tools
		}
		if len(t.PerToolTimeout) > 0 {
			if in.Policy.PerToolTimeout == nil {
				in.Policy.PerToolTimeout = make(map[tools.Ident]time.Duration, len(t.PerToolTimeout))
			}
			for k, v := range t.PerToolTimeout {
				in.Policy.PerToolTimeout[k] = v
			}
		}
	}
}

// WithPerTurnMaxToolCalls sets a per-turn cap on tool executions. Zero means unlimited.
func WithPerTurnMaxToolCalls(n int) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.PerTurnMaxToolCalls = n
	}
}

// WithRunMaxToolCalls sets a per-run cap on total tool executions.
func WithRunMaxToolCalls(n int) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.MaxToolCalls = n
	}
}

// WithRunMaxConsecutiveFailedToolCalls caps consecutive failures before aborting the run.
func WithRunMaxConsecutiveFailedToolCalls(n int) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.MaxConsecutiveFailedToolCalls = n
	}
}

// WithRunTimeBudget sets the semantic wall-clock budget for planner and tool work in the run.
func WithRunTimeBudget(d time.Duration) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.TimeBudget = d
	}
}

// WithRunFinalizerGrace reserves time to produce a final assistant message after the run budget is exhausted.
func WithRunFinalizerGrace(d time.Duration) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.FinalizerGrace = d
	}
}

// WithRunInterruptsAllowed enables human-in-the-loop interruptions for this run.
func WithRunInterruptsAllowed(allowed bool) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.InterruptsAllowed = allowed
	}
}

// WithRestrictToTool restricts candidate tools to a single tool for the run.
func WithRestrictToTool(id tools.Ident) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.RestrictToTool = id
	}
}

// WithAllowedTags filters candidate tools to those whose tags intersect this list.
func WithAllowedTags(tags []string) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.AllowedTags = append([]string(nil), tags...)
	}
}

// WithDeniedTags filters out candidate tools that have any of these tags.
func WithDeniedTags(tags []string) RunOption {
	return func(in *RunInput) {
		if in.Policy == nil {
			in.Policy = &PolicyOverrides{}
		}
		in.Policy.DeniedTags = append([]string(nil), tags...)
	}
}

// newFromOptions constructs a Runtime using the provided options.
func newFromOptions(opts Options) *Runtime {
	if opts.ToolConfirmation != nil {
		if err := opts.ToolConfirmation.validate(); err != nil {
			panic(err)
		}
	}
	bus := opts.Hooks
	if bus == nil {
		bus = hooks.NewBus()
	}
	eng := opts.Engine
	if eng == nil {
		eng = engineinmem.New()
	}
	metrics := opts.Metrics
	if metrics == nil {
		metrics = telemetry.NoopMetrics{}
	}
	logger := opts.Logger
	if logger == nil {
		logger = telemetry.NoopLogger{}
	}
	tracer := opts.Tracer
	if tracer == nil {
		tracer = telemetry.NoopTracer{}
	}
	if opts.RunEventStore == nil {
		opts.RunEventStore = runloginmem.New()
	}
	if opts.SessionStore == nil {
		opts.SessionStore = sessioninmem.New()
	}
	rt := &Runtime{
		Engine:              eng,
		Memory:              opts.MemoryStore,
		PromptRegistry:      prompt.NewRegistry(opts.PromptStore),
		SessionStore:        opts.SessionStore,
		Policy:              opts.Policy,
		RunEventStore:       opts.RunEventStore,
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

func (r *Runtime) installRuntimeSubscribers(bus hooks.Bus) {
	r.mu.Lock()
	r.addToolsetLocked(toolUnavailableToolsetRegistration())
	r.mu.Unlock()
	if r.SessionStore != nil {
		r.registerSessionSubscriber(bus)
	}
	if r.Memory != nil {
		r.registerMemorySubscriber(bus)
	}
	if r.Stream != nil {
		streamSub, err := stream.NewSubscriber(newHintingSink(r, r.Stream))
		if err != nil {
			r.logger.Warn(context.Background(), "failed to create stream subscriber", "err", err)
		} else {
			r.streamSubscriber = streamSub
		}
	}
}

func (r *Runtime) registerSessionSubscriber(bus hooks.Bus) {
	sessionSub := hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		if event.SessionID() == "" {
			return nil
		}
		var (
			status   session.RunStatus
			metadata map[string]any
		)
		ts := time.UnixMilli(event.Timestamp()).UTC()
		switch evt := event.(type) {
		case *hooks.RunStartedEvent:
			status = session.RunStatusRunning
			return r.SessionStore.UpsertRun(ctx, session.RunMeta{
				AgentID:   evt.AgentID(),
				RunID:     evt.RunID(),
				SessionID: evt.SessionID(),
				Status:    status,
				UpdatedAt: ts,
				Labels:    evt.RunContext.Labels,
				Metadata:  nil,
				StartedAt: time.Time{},
			})
		case *hooks.RunPausedEvent:
			status = session.RunStatusPaused
			return r.SessionStore.UpsertRun(ctx, session.RunMeta{
				AgentID:   evt.AgentID(),
				RunID:     evt.RunID(),
				SessionID: evt.SessionID(),
				Status:    status,
				UpdatedAt: ts,
				Labels:    evt.Labels,
				Metadata:  evt.Metadata,
			})
		case *hooks.RunResumedEvent:
			status = session.RunStatusRunning
			return r.SessionStore.UpsertRun(ctx, session.RunMeta{
				AgentID:   evt.AgentID(),
				RunID:     evt.RunID(),
				SessionID: evt.SessionID(),
				Status:    status,
				UpdatedAt: ts,
				Labels:    evt.Labels,
			})
		case *hooks.RunCompletedEvent:
			switch evt.Status {
			case "success":
				status = session.RunStatusCompleted
			case "failed":
				status = session.RunStatusFailed
			case "canceled":
				status = session.RunStatusCanceled
			default:
				return fmt.Errorf("unexpected run completed status %q", evt.Status)
			}
			if evt.PublicError != "" {
				metadata = map[string]any{
					"public_error": evt.PublicError,
				}
				if evt.ErrorProvider != "" {
					metadata["error_provider"] = evt.ErrorProvider
				}
				if evt.ErrorOperation != "" {
					metadata["error_operation"] = evt.ErrorOperation
				}
				if evt.ErrorKind != "" {
					metadata["error_kind"] = evt.ErrorKind
				}
				if evt.ErrorCode != "" {
					metadata["error_code"] = evt.ErrorCode
				}
				if evt.HTTPStatus != 0 {
					metadata["http_status"] = evt.HTTPStatus
				}
				metadata["retryable"] = evt.Retryable
			}
			return r.SessionStore.UpsertRun(ctx, session.RunMeta{
				AgentID:   evt.AgentID(),
				RunID:     evt.RunID(),
				SessionID: evt.SessionID(),
				Status:    status,
				UpdatedAt: ts,
				Metadata:  metadata,
			})
		default:
			return nil
		}
	})
	if _, err := bus.Register(sessionSub); err != nil {
		r.logger.Warn(context.Background(), "failed to register session subscriber", "err", err)
	}
}

func (r *Runtime) registerMemorySubscriber(bus hooks.Bus) {
	memSub := hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		var memEvent memory.Event
		switch evt := event.(type) {
		case *hooks.ToolCallScheduledEvent:
			memEvent = memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.ToolCallData{
				ToolCallID:            evt.ToolCallID,
				ParentToolCallID:      evt.ParentToolCallID,
				ToolName:              evt.ToolName,
				PayloadJSON:           evt.Payload,
				Queue:                 evt.Queue,
				ExpectedChildrenTotal: evt.ExpectedChildrenTotal,
			}, nil)
			return r.Memory.AppendEvents(ctx, evt.AgentID(), evt.RunID(), memEvent)
		case *hooks.ToolResultReceivedEvent:
			errorMessage := ""
			if evt.Error != nil {
				errorMessage = evt.Error.Error()
			}
			memEvent = memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.ToolResultData{
				ToolCallID:       evt.ToolCallID,
				ParentToolCallID: evt.ParentToolCallID,
				ToolName:         evt.ToolName,
				ResultJSON:       evt.ResultJSON,
				Preview:          evt.ResultPreview,
				Bounds:           evt.Bounds,
				Duration:         evt.Duration,
				ErrorMessage:     errorMessage,
			}, nil)
			return r.Memory.AppendEvents(ctx, evt.AgentID(), evt.RunID(), memEvent)
		case *hooks.AssistantMessageEvent:
			memEvent = memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.AssistantMessageData{
				Message:    evt.Message,
				Structured: evt.Structured,
			}, nil)
			return r.Memory.AppendEvents(ctx, evt.AgentID(), evt.RunID(), memEvent)
		case *hooks.ThinkingBlockEvent:
			memEvent = memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.ThinkingData{
				Text:         evt.Text,
				Signature:    evt.Signature,
				Redacted:     evt.Redacted,
				ContentIndex: evt.ContentIndex,
				Final:        evt.Final,
			}, nil)
			return r.Memory.AppendEvents(ctx, evt.AgentID(), evt.RunID(), memEvent)
		case *hooks.PlannerNoteEvent:
			memEvent = memory.NewEvent(time.UnixMilli(evt.Timestamp()), memory.PlannerNoteData{
				Note: evt.Note,
			}, evt.Labels)
			return r.Memory.AppendEvents(ctx, evt.AgentID(), evt.RunID(), memEvent)
		}
		return nil
	})
	if _, err := bus.Register(memSub); err != nil {
		r.logger.Warn(context.Background(), "failed to register memory subscriber", "err", err)
	}
}

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

// RegisterModel registers a ModelClient by identifier for planner lookup.
func (r *Runtime) RegisterModel(id string, client model.Client) error {
	if id == "" {
		return errors.New("model id is required")
	}
	if client == nil {
		return errors.New("model client is required")
	}
	r.mu.Lock()
	r.models[id] = client
	r.mu.Unlock()
	return nil
}

// ModelClient returns a registered model client by ID, if present.
func (r *Runtime) ModelClient(id string) (model.Client, bool) {
	if id == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	return m, ok
}

// BedrockConfig configures the bedrock-backed model client created by the runtime.
type BedrockConfig struct {
	DefaultModel   string
	HighModel      string
	SmallModel     string
	MaxTokens      int
	ThinkingBudget int
	Temperature    float32
}

// NewBedrockModelClient constructs a model.Client backed by AWS Bedrock using the
// runtime's own ledger access.
func (r *Runtime) NewBedrockModelClient(awsrt *bedrockruntime.Client, cfg BedrockConfig) (model.Client, error) {
	opts := bedrock.Options{
		Runtime:        awsrt,
		DefaultModel:   cfg.DefaultModel,
		HighModel:      cfg.HighModel,
		SmallModel:     cfg.SmallModel,
		MaxTokens:      cfg.MaxTokens,
		ThinkingBudget: cfg.ThinkingBudget,
		Temperature:    cfg.Temperature,
		Logger:         r.logger,
	}
	if querier, ok := r.Engine.(bedrock.WorkflowQuerier); ok {
		return bedrock.New(awsrt, opts, bedrock.NewTemporalLedgerSource(querier))
	}
	return bedrock.New(awsrt, opts, nil)
}
