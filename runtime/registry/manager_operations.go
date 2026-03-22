package registry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"goa.design/goa-ai/runtime/agent/telemetry"
)

type observedOperation struct {
	manager     *Manager
	ctx         context.Context
	span        telemetry.Span
	start       time.Time
	event       OperationEvent
	resultCount *int
	err         *error
	outcome     *OperationOutcome
}

func (m *Manager) observeOperation(
	ctx context.Context,
	event OperationEvent,
	resultCount *int,
	err *error,
	outcome *OperationOutcome,
	attrs ...attribute.KeyValue,
) *observedOperation {
	ctx, span := m.obs.StartSpan(ctx, event.Operation, attrs...)
	return &observedOperation{
		manager:     m,
		ctx:         ctx,
		span:        span,
		start:       time.Now(),
		event:       event,
		resultCount: resultCount,
		err:         err,
		outcome:     outcome,
	}
}

func (o *observedOperation) finish() {
	o.event.Duration = time.Since(o.start)
	if o.outcome != nil {
		o.event.Outcome = *o.outcome
	}
	if o.err != nil && *o.err != nil {
		o.event.Error = (*o.err).Error()
	}
	if o.resultCount != nil {
		o.event.ResultCount = *o.resultCount
	}
	o.manager.obs.LogOperation(o.ctx, o.event)
	o.manager.obs.RecordOperationMetrics(o.event)
	if o.outcome != nil {
		o.manager.obs.EndSpan(o.span, *o.outcome, valueOrNil(o.err))
	}
}

func valueOrNil(err *error) error {
	if err == nil {
		return nil
	}
	return *err
}
