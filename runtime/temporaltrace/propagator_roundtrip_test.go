package temporaltrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type traceHeaders map[string]*commonpb.Payload

// This roundtrip models a request -> workflow -> activity boundary: the origin
// remains a link even when the intermediate workflow has no live request span.
func TestTraceOriginSurvivesWorkflowBoundary(t *testing.T) {
	for _, tc := range []struct{ name, origin string }{
		{"valid", validTestTraceParent}, {"absent", ""}, {"invalid", "not-a-trace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			propagator := NewLinkPropagator()
			headers := traceHeaders{}
			if tc.origin != "" {
				payload, err := converter.GetDefaultDataConverter().ToPayload(tc.origin)
				require.NoError(t, err)
				headers.Set(HeaderTraceParent, payload)
			}
			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				received, err := propagator.ExtractToWorkflow(ctx, headers)
				if err != nil {
					return "", err
				}
				forwarded := traceHeaders{}
				if err := propagator.InjectFromWorkflow(received, forwarded); err != nil {
					return "", err
				}
				activityCtx, err := propagator.Extract(context.Background(), forwarded)
				if err != nil {
					return "", err
				}
				origin, _ := OriginTraceParent(activityCtx)
				return origin, nil
			})
			require.NoError(t, env.GetWorkflowError())
			var got string
			require.NoError(t, env.GetWorkflowResult(&got))
			want := ""
			if tc.name == "valid" {
				want = validTestTraceParent
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestTracePropagatorUsesActiveSpanAndRejectsCorruptPayload(t *testing.T) {
	p := NewLinkPropagator()
	sc, err := ParseTraceParent(validTestTraceParent)
	require.NoError(t, err)
	headers := traceHeaders{}
	require.NoError(t, p.Inject(trace.ContextWithSpanContext(t.Context(), sc), headers))
	ctx, err := p.Extract(t.Context(), headers)
	require.NoError(t, err)
	origin, ok := OriginTraceParent(ctx)
	assert.True(t, ok)
	assert.Equal(t, validTestTraceParent, origin)
	headers[HeaderTraceParent] = &commonpb.Payload{Metadata: map[string][]byte{"encoding": []byte("json/plain")}, Data: []byte("{")}
	_, err = p.Extract(t.Context(), headers)
	require.ErrorContains(t, err, "decode traceparent")
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		_, err := p.ExtractToWorkflow(ctx, headers)
		return err
	})
	require.ErrorContains(t, env.GetWorkflowError(), "decode traceparent")
}

func (h traceHeaders) Get(key string) (*commonpb.Payload, bool) {
	value, ok := h[key]
	return value, ok
}

func (h traceHeaders) Set(key string, value *commonpb.Payload) {
	h[key] = value
}

func (h traceHeaders) ForEachKey(handler func(string, *commonpb.Payload) error) error {
	for key, value := range h {
		if err := handler(key, value); err != nil {
			return err
		}
	}
	return nil
}
