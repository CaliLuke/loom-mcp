package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type (
	runtimeMetricRecord struct {
		name string
		tags []string
	}

	runtimeTelemetryRecorder struct {
		mu       sync.Mutex
		counters []runtimeMetricRecord
		timers   []runtimeMetricRecord
		spans    []*runtimeSpanRecord
	}

	runtimeSpanRecord struct {
		mu         sync.Mutex
		name       string
		attributes map[string]attribute.Value
		status     codes.Code
		errors     []error
		ended      bool
	}
)

func TestPlannerTelemetryRecordsStableAttemptsAndOutcome(t *testing.T) {
	recorder := &runtimeTelemetryRecorder{}
	wantErr := errors.New("planner failed")
	rt := New(WithMetrics(recorder), WithTracer(recorder))
	reg := &AgentRegistration{
		ID: agent.Ident("service.agent"),
		Planner: &stubPlanner{
			start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{}, nil
			},
			resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
				return nil, wantErr
			},
		},
	}
	runCtx := run.Context{RunID: "run-1", SessionID: "session-1", TurnID: "turn-1"}

	_, err := rt.planStart(context.Background(), reg, &planner.PlanInput{RunContext: runCtx})
	require.NoError(t, err)
	_, err = rt.planResume(context.Background(), reg, &planner.PlanResumeInput{RunContext: runCtx})
	require.ErrorIs(t, err, wantErr)

	assert.Equal(t, []runtimeMetricRecord{
		{name: runtimePlannerAttemptsMetric, tags: []string{"agent", "service.agent", "operation", "start", "status", "success"}},
		{name: runtimePlannerAttemptsMetric, tags: []string{"agent", "service.agent", "operation", "resume", "status", "error"}},
	}, recorder.counterRecords())
	assert.Equal(t, []runtimeMetricRecord{
		{name: runtimePlannerDurationMetric, tags: []string{"agent", "service.agent", "operation", "start", "status", "success"}},
		{name: runtimePlannerDurationMetric, tags: []string{"agent", "service.agent", "operation", "resume", "status", "error"}},
	}, recorder.timerRecords())

	spans := recorder.spanRecords()
	require.Len(t, spans, 2)
	assert.Equal(t, "planner.plan_start", spans[0].name)
	assert.Equal(t, codes.Ok, spans[0].status)
	assert.Equal(t, "start", spans[0].attributes["loom_mcp.planner.operation"].AsString())
	assert.Equal(t, "run-1", spans[0].attributes["loom_mcp.run_id"].AsString())
	assert.True(t, spans[0].ended)
	assert.Equal(t, "planner.plan_resume", spans[1].name)
	assert.Equal(t, codes.Error, spans[1].status)
	assert.Equal(t, []error{wantErr}, spans[1].errors)
	assert.True(t, spans[1].ended)
}

func TestCanonicalEventTelemetryDeduplicatesHookRetries(t *testing.T) {
	recorder := &runtimeTelemetryRecorder{}
	rt := New(WithMetrics(recorder), WithTracer(recorder))
	rt.toolSpecs[tools.Ident("lookup")] = tools.ToolSpec{Name: tools.Ident("lookup")}

	toolEvent := hooks.NewToolResultReceivedEvent(
		"run-1",
		agent.Ident("service.agent"),
		"session-1",
		tools.Ident("lookup"),
		"call-1",
		"",
		map[string]any{"ok": true},
		nil,
		nil,
		"",
		nil,
		125*time.Millisecond,
		nil,
		nil,
		nil,
	)
	input, err := hooks.EncodeToHookInput(toolEvent, "turn-1")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(context.Background(), input))
	require.NoError(t, rt.hookActivity(context.Background(), input))

	assert.Equal(t, []runtimeMetricRecord{{
		name: runtimeToolCompletedMetric,
		tags: []string{"agent", "service.agent", "tool", "lookup", "status", "success"},
	}}, recorder.counterRecords())
	assert.Equal(t, []runtimeMetricRecord{{
		name: runtimeToolDurationMetric,
		tags: []string{"agent", "service.agent", "tool", "lookup", "status", "success"},
	}}, recorder.timerRecords())
	spans := recorder.spanRecords()
	require.Len(t, spans, 1)
	assert.Equal(t, "tool.execute", spans[0].name)
	assert.Equal(t, codes.Ok, spans[0].status)
	assert.Equal(t, "lookup", spans[0].attributes["loom_mcp.tool.name"].AsString())
	assert.Equal(t, "call-1", spans[0].attributes["loom_mcp.tool_call_id"].AsString())
	assert.Equal(t, int64(125), spans[0].attributes["loom_mcp.tool.duration_ms"].AsInt64())
	assert.True(t, spans[0].ended)
}

func TestCanonicalRunMetricsRecordLifecycleStatus(t *testing.T) {
	recorder := &runtimeTelemetryRecorder{}
	rt := New(WithMetrics(recorder), WithTracer(recorder))
	runCtx := run.Context{RunID: "run-1"}
	events := []hooks.Event{
		hooks.NewRunStartedEvent("run-1", agent.Ident("service.agent"), runCtx, struct{}{}),
		hooks.NewRunCompletedEvent("run-1", agent.Ident("service.agent"), "", runStatusSuccess, run.PhaseCompleted, nil),
	}
	for _, event := range events {
		input, err := hooks.EncodeToHookInput(event, "turn-1")
		require.NoError(t, err)
		require.NoError(t, rt.hookActivity(context.Background(), input))
	}

	assert.Equal(t, []runtimeMetricRecord{
		{name: runtimeRunStartedMetric, tags: []string{"agent", "service.agent"}},
		{name: runtimeRunCompletedMetric, tags: []string{"agent", "service.agent", "status", "success"}},
	}, recorder.counterRecords())
}

func TestToolMetricsNormalizeUnregisteredNames(t *testing.T) {
	recorder := &runtimeTelemetryRecorder{}
	rt := New(WithMetrics(recorder), WithTracer(recorder))
	event := hooks.NewToolResultReceivedEvent(
		"run-1",
		agent.Ident("service.agent"),
		"",
		tools.Ident("model-controlled-name"),
		"call-1",
		"",
		nil,
		nil,
		nil,
		"",
		nil,
		0,
		nil,
		nil,
		nil,
	)

	rt.recordToolResultTelemetry(context.Background(), event)

	assert.Equal(t, []runtimeMetricRecord{{
		name: runtimeToolCompletedMetric,
		tags: []string{"agent", "service.agent", "tool", "unknown", "status", "success"},
	}}, recorder.counterRecords())
}

func (r *runtimeTelemetryRecorder) IncCounter(name string, _ float64, tags ...string) {
	r.mu.Lock()
	r.counters = append(r.counters, runtimeMetricRecord{name: name, tags: append([]string(nil), tags...)})
	r.mu.Unlock()
}

func (r *runtimeTelemetryRecorder) RecordTimer(name string, _ time.Duration, tags ...string) {
	r.mu.Lock()
	r.timers = append(r.timers, runtimeMetricRecord{name: name, tags: append([]string(nil), tags...)})
	r.mu.Unlock()
}

func (*runtimeTelemetryRecorder) RecordGauge(string, float64, ...string) {}

func (r *runtimeTelemetryRecorder) Start(ctx context.Context, name string, _ ...trace.SpanStartOption) (context.Context, telemetry.Span) {
	span := &runtimeSpanRecord{name: name, attributes: make(map[string]attribute.Value)}
	r.mu.Lock()
	r.spans = append(r.spans, span)
	r.mu.Unlock()
	return ctx, span
}

func (*runtimeTelemetryRecorder) Span(context.Context) telemetry.Span {
	return telemetry.NoopTracer{}.Span(context.Background())
}

func (r *runtimeTelemetryRecorder) counterRecords() []runtimeMetricRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtimeMetricRecord(nil), r.counters...)
}

func (r *runtimeTelemetryRecorder) timerRecords() []runtimeMetricRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]runtimeMetricRecord(nil), r.timers...)
}

func (r *runtimeTelemetryRecorder) spanRecords() []*runtimeSpanRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*runtimeSpanRecord(nil), r.spans...)
}

func (s *runtimeSpanRecord) End(...trace.SpanEndOption) {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
}

func (*runtimeSpanRecord) AddEvent(string, ...any) {}

func (s *runtimeSpanRecord) SetAttributes(attrs ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, attr := range attrs {
		s.attributes[string(attr.Key)] = attr.Value
	}
}

func (s *runtimeSpanRecord) SetStatus(code codes.Code, _ string) {
	s.mu.Lock()
	s.status = code
	s.mu.Unlock()
}

func (s *runtimeSpanRecord) RecordError(err error, _ ...trace.EventOption) {
	s.mu.Lock()
	s.errors = append(s.errors, err)
	s.mu.Unlock()
}
