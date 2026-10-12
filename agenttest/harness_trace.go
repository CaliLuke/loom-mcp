package agenttest

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"go.opentelemetry.io/otel/trace"
)

// SessionResult holds caller-ordered runs in one new session.
type SessionResult struct {
	SessionID  string
	Runs       []*Result
	Trajectory trajectory.Session
}

// WithTracerProvider supplies an isolated provider for root and runtime spans.
// The harness never sets or shuts down the global or supplied provider.
func WithTracerProvider(provider trace.TracerProvider) Option {
	return func(options *harnessOptions) error {
		options.provider = provider
		return nil
	}
}

// RunSession runs turns sequentially in one new session, stopping at the first
// harness error and returning the completed prefix. Agent failures are outcomes.
// Prior history comes only from the agent's memory policy; the harness carries
// no messages between turns.
func (h *Harness) RunSession(ctx context.Context, id agent.Ident, turns ...Input) (*SessionResult, error) {
	session, err := h.OpenSession(ctx)
	if err != nil {
		return nil, err
	}
	for i, input := range turns {
		if _, err := session.Run(ctx, id, input); err != nil {
			result := session.Result()
			return result, fmt.Errorf("agenttest: session %s turn %d: %w", result.SessionID, i+1, err)
		}
	}
	return session.Result(), nil
}

func configureTracer(options *harnessOptions) telemetry.Tracer {
	if options.provider == nil {
		return telemetry.NewNoopTracer()
	}
	return telemetry.NewTracer(options.provider)
}

func (h *Harness) startTrace(ctx context.Context) (context.Context, string, func()) {
	if h.provider == nil {
		return ctx, "", func() {
			// No span was opened.
		}
	}
	ctx, span := h.provider.Tracer("github.com/CaliLuke/loom-mcp/v2/agenttest").Start(ctx, "agenttest.run")
	return ctx, span.SpanContext().TraceID().String(), func() {
		span.End()
	}
}
