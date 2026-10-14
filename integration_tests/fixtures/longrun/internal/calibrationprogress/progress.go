// Package calibrationprogress renders bounded operator progress independently of evidence.
package calibrationprogress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// Reporter writes phase changes and a 30-second heartbeat to the supplied output.
// It never writes calibration artifacts or changes statistical inputs.
type Reporter struct {
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	output       io.Writer
	stop         chan struct{}
	done         chan struct{}
	started      time.Time
	phaseStarted time.Time
	phase        string
	progress     gate.CalibrationProgress
	hasProgress  bool
	err          error
}

// Start begins progress reporting. Writer failures cancel Context so computation
// cannot continue silently for hours after losing its requested progress output.
func Start(ctx context.Context, output io.Writer) *Reporter {
	ctx, cancel := context.WithCancel(ctx)
	now := time.Now()
	r := &Reporter{ctx: ctx, cancel: cancel, output: output, stop: make(chan struct{}), done: make(chan struct{}), started: now, phaseStarted: now, phase: "preflight"}
	r.emit("running", now)
	go r.run()
	return r
}

// Context cancels when the caller cancels or progress output fails.
func (r *Reporter) Context() context.Context { return r.ctx }

// Phase flushes the preceding phase's final counters and resets phase timing.
func (r *Reporter) Phase(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.hasProgress {
		r.emit("running", now)
	}
	r.phase = name
	r.phaseStarted = now
	r.progress = gate.CalibrationProgress{}
	r.hasProgress = false
	r.emit("running", now)
}

// Observe records actual simulation work. Individual simulations never flood
// the output: only the initial totals and timed heartbeat are written.
func (r *Reporter) Observe(value gate.CalibrationProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progress = value
	if !r.hasProgress {
		r.hasProgress = true
		r.emit("running", time.Now())
	}
}

// Close stops the heartbeat and reports the final outcome. Call it exactly once,
// after evidence verification and artifact writes, including on failure paths.
// The returned error retains the work, cancellation and output failures.
func (r *Reporter) Close(workErr error) error {
	close(r.stop)
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.cancel()
	err := errors.Join(workErr, r.err, r.ctx.Err())
	status := "complete"
	if err != nil {
		status = "failed"
		if r.err == nil && errors.Is(err, context.Canceled) {
			status = "canceled"
		}
	}
	r.emit(status, time.Now())
	return errors.Join(err, r.err)
}

func (r *Reporter) run() {
	defer close(r.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case now := <-ticker.C:
			r.mu.Lock()
			r.emit("running", now)
			r.mu.Unlock()
		}
	}
}

func (r *Reporter) emit(status string, now time.Time) {
	if r.err != nil {
		return
	}
	elapsed := max(time.Duration(0), now.Sub(r.phaseStarted))
	rate, percent := 0.0, 0.0
	eta := "eta=unknown"
	p := r.progress
	if p.SimulationsTotal > 0 {
		percent = 100 * float64(p.SimulationsCompleted) / float64(p.SimulationsTotal)
	}
	if elapsed > 0 && p.SimulationsCompleted > 0 {
		rate = float64(p.SimulationsCompleted) / elapsed.Seconds()
		remaining := time.Duration(float64(p.SimulationsTotal-p.SimulationsCompleted) / rate * float64(time.Second))
		eta = "eta~" + remaining.Round(time.Second).String()
	}
	if r.hasProgress && p.CellsCompleted == p.CellsTotal {
		percent = 100
		eta = "eta~0s"
	}
	_, err := fmt.Fprintf(r.output, "calibration status=%s phase=%q cells=%d/%d simulations=%d/%d progress=%.1f%% elapsed=%s phase_elapsed=%s rate=%.2f/s %s\n", status, r.phase, p.CellsCompleted, p.CellsTotal, p.SimulationsCompleted, p.SimulationsTotal, percent, now.Sub(r.started).Round(time.Second), elapsed.Round(time.Second), rate, eta)
	if err != nil {
		r.err = fmt.Errorf("write calibration progress: %w", err)
		r.cancel()
	}
}
