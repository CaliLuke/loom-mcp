package temporaltrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const validTestTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// recordingTracer captures the span start configuration of every span it
// creates so tests can assert on links without a full OTel SDK pipeline.
type recordingTracer struct {
	noop.Tracer

	configs []trace.SpanConfig
}

// headerWriter is a minimal workflow.HeaderWriter backed by a map.
type headerWriter map[string]*commonpb.Payload

func (t *recordingTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	t.configs = append(t.configs, trace.NewSpanStartConfig(opts...))
	return t.Tracer.Start(ctx, name, opts...)
}

func (w headerWriter) Set(key string, value *commonpb.Payload) {
	w[key] = value
}

func TestParseTraceParent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		traceparent string
		wantErr     bool
	}{
		{
			name:        "valid",
			traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
		{
			name:        "invalid_empty",
			traceparent: "",
			wantErr:     true,
		},
		{
			name:        "invalid_version_ff",
			traceparent: "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			wantErr:     true,
		},
		{
			name:        "invalid_trace_id_length",
			traceparent: "00-4bf92f3577b34da6a3ce929d0e0e473-00f067aa0ba902b7-01",
			wantErr:     true,
		},
		{
			name:        "invalid_span_id_length",
			traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902-01",
			wantErr:     true,
		},
		{
			name:        "invalid_flags_length",
			traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-0",
			wantErr:     true,
		},
		{
			name:        "invalid_hex",
			traceparent: "00-4bf92f3577b34da6a3ce929d0e0e473z-00f067aa0ba902b7-01",
			wantErr:     true,
		},
		{
			name:        "invalid_shape_version_00_extra_parts",
			traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra",
			wantErr:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sc, err := ParseTraceParent(tc.traceparent)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (spanContext=%v)", sc)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			if !sc.IsValid() {
				t.Fatalf("expected valid span context")
			}
			if !sc.IsRemote() {
				t.Fatalf("expected remote span context")
			}
		})
	}
}

func TestWithOriginTraceParentDropsInvalidValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		traceparent string
		wantStored  bool
	}{
		{
			name:        "valid",
			traceparent: validTestTraceParent,
			wantStored:  true,
		},
		{
			name:        "garbage",
			traceparent: "not-a-traceparent",
			wantStored:  false,
		},
		{
			name:        "empty",
			traceparent: "",
			wantStored:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := WithOriginTraceParent(context.Background(), tc.traceparent)
			got, ok := OriginTraceParent(ctx)
			assert.Equal(t, tc.wantStored, ok)
			if tc.wantStored {
				assert.Equal(t, tc.traceparent, got)
			}
		})
	}
}

func TestInjectSkipsInvalidOriginTraceParent(t *testing.T) {
	t.Parallel()

	propagator := NewLinkPropagator()
	ctx := WithOriginTraceParent(context.Background(), "not-a-traceparent")
	writer := headerWriter{}
	require.NoError(t, propagator.Inject(ctx, writer))
	assert.NotContains(t, writer, HeaderTraceParent)
}

func TestInjectCarriesValidOriginTraceParent(t *testing.T) {
	t.Parallel()

	propagator := NewLinkPropagator()
	ctx := WithOriginTraceParent(context.Background(), validTestTraceParent)
	writer := headerWriter{}
	require.NoError(t, propagator.Inject(ctx, writer))
	assert.Contains(t, writer, HeaderTraceParent)
}

func TestExecuteActivityToleratesMalformedOriginTraceParent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		traceparent string
		wantLink    bool
	}{
		{
			name:        "malformed traceparent still executes without link",
			traceparent: "not-a-traceparent",
			wantLink:    false,
		},
		{
			name:        "valid traceparent links origin trace",
			traceparent: validTestTraceParent,
			wantLink:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracer := &recordingTracer{}
			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestActivityEnvironment()
			// Store the traceparent under the origin key directly so the test
			// exercises the interceptor's own fail-open path even for values
			// that WithOriginTraceParent would reject.
			env.SetWorkerOptions(worker.Options{
				BackgroundActivityContext: context.WithValue(
					context.Background(), originTraceParentKey{}, tc.traceparent,
				),
				Interceptors: []interceptor.WorkerInterceptor{
					&ActivityInterceptor{Tracer: tracer},
				},
			})

			executed := false
			activityFn := func(ctx context.Context) (string, error) {
				executed = true
				return "ok", nil
			}
			env.RegisterActivity(activityFn)

			_, err := env.ExecuteActivity(activityFn)
			require.NoError(t, err)
			assert.True(t, executed, "activity must execute")

			require.Len(t, tracer.configs, 1)
			links := tracer.configs[0].Links()
			if !tc.wantLink {
				assert.Empty(t, links)
				return
			}
			require.Len(t, links, 1)
			origin, parseErr := ParseTraceParent(tc.traceparent)
			require.NoError(t, parseErr)
			assert.Equal(t, origin.TraceID(), links[0].SpanContext.TraceID())
			assert.Equal(t, origin.SpanID(), links[0].SpanContext.SpanID())
		})
	}
}
