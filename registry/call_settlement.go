package registry

import (
	"context"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

type callSettlementTracker struct {
	store  *callAdmissionStore
	logger telemetry.Logger

	ctx       context.Context
	cancel    context.CancelFunc
	doneCh    chan struct{}
	closeOnce sync.Once
}

const (
	callSettlementInterval  = 250 * time.Millisecond
	callSettlementBatchSize = 128
)

func newCallSettlementTracker(ctx context.Context, store *callAdmissionStore, logger telemetry.Logger) *callSettlementTracker {
	if logger == nil {
		logger = telemetry.NewNoopLogger()
	}
	trackerCtx, cancel := context.WithCancel(ctx)
	tracker := &callSettlementTracker{
		store:  store,
		logger: logger,
		ctx:    trackerCtx,
		cancel: cancel,
		doneCh: make(chan struct{}),
	}
	go tracker.run()
	return tracker
}

func (t *callSettlementTracker) Close() {
	t.closeOnce.Do(func() {
		t.cancel()
		<-t.doneCh
	})
}

func (t *callSettlementTracker) run() {
	defer close(t.doneCh)
	t.settle()
	ticker := time.NewTicker(callSettlementInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			t.settle()
		}
	}
}

func (t *callSettlementTracker) settle() {
	for {
		if err := t.ctx.Err(); err != nil {
			return
		}
		settled, err := t.store.SettleLostClaims(t.ctx, callSettlementBatchSize)
		if err != nil {
			if t.ctx.Err() != nil {
				return
			}
			t.logger.Error(
				t.ctx,
				"settle lost tool call claims failed",
				"event", "settle_lost_tool_call_claims_failed",
				"component", "tool-registry-settlement",
				"err", err,
			)
			return
		}
		if settled < callSettlementBatchSize {
			return
		}
	}
}
