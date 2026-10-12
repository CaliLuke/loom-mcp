package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rb8Engine gates actual in-memory activity execution and query snapshots.
// It does not replace workflow signal delivery or result adoption.
type rb8Engine struct {
	engine.Engine
	signals    atomic.Int32
	records    atomic.Int32
	queries    atomic.Int32
	queryGate  chan struct{}
	taken      chan struct{}
	recordGate chan struct{}
}

type rb8Hooks struct {
	*recordingHooks
	paused  chan struct{}
	release chan struct{}
	once    sync.Once
}

func TestProvideToolResultsRejectsDuplicateHead(t *testing.T) {
	for _, kind := range []string{"questions", "external"} {
		for _, phase := range []string{"queued", "taken", "consumed"} {
			t.Run(kind+"/"+phase, func(t *testing.T) {
				rt, eng, bus, ctx := rb8Runtime(t, kind, false)
				if phase == "taken" {
					eng.taken = make(chan struct{})
					eng.recordGate = make(chan struct{})
					defer close(eng.recordGate)
				}
				handle, err := rt.MustClient("content.agent").Start(ctx, "session", nil, WithRunID("root"))
				require.NoError(t, err)
				rb8Wait(t, ctx, bus.paused)
				require.NoError(t, rt.ProvideToolResults(ctx, rb7Answer("first")))
				if phase != "queued" {
					close(bus.release)
				}
				if phase == "taken" {
					rb8Wait(t, ctx, eng.taken)
				}
				if phase == "consumed" {
					rb8WaitSecondAwait(t, ctx, eng)
				}
				store := rt.RunStore.(*contentPathStore)
				before := store.snapshot()
				err = rt.ProvideToolResults(ctx, rb7Answer("first"))
				var notReady *ToolAwaitNotReadyError
				require.ErrorAs(t, err, &notReady)
				assert.Equal(t, "first", notReady.ID)
				assertToolResultContractError(t, err)
				assert.Equal(t, int32(1), eng.signals.Load(), "rejection must not signal")
				assert.Equal(t, before, store.snapshot(), "rejection must not write content")
				if phase == "queued" {
					close(bus.release)
				}
				if phase == "taken" {
					eng.recordGate <- struct{}{}
				}
				rb8WaitSecondAwait(t, ctx, eng)
				require.NoError(t, rt.ProvideToolResults(ctx, rb7Answer("second")))
				out, err := handle.Wait(ctx)
				require.NoError(t, err)
				rb8AssertCompleted(t, rt, eng, bus, out)
			})
		}
	}
}

func TestProvidedToolResultsIgnoresDuplicateSignals(t *testing.T) {
	for _, kind := range []string{"questions", "external"} {
		for _, acrossTurns := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/acrossTurns=%v", kind, acrossTurns), func(t *testing.T) {
				rt, eng, bus, ctx := rb8Runtime(t, kind, acrossTurns)
				eng.taken = make(chan struct{})
				eng.recordGate = make(chan struct{})
				defer close(eng.recordGate)
				handle, err := rt.MustClient("content.agent").Start(ctx, "session", nil, WithRunID("root"))
				require.NoError(t, err)
				rb8Wait(t, ctx, bus.paused)
				require.NoError(t, rt.ProvideToolResults(ctx, rb7Answer("first")))
				close(bus.release)
				rb8Wait(t, ctx, eng.taken)
				// No results: a duplicate must be ignored before validation or recording.
				require.NoError(t, eng.SignalByID(ctx, "root", "", api.SignalProvideToolResults, &api.ToolResultsSet{ID: "first"}))
				eng.recordGate <- struct{}{}
				rb8WaitSecondAwait(t, ctx, eng)
				require.NoError(t, eng.SignalByID(ctx, "root", "", api.SignalProvideToolResults, &api.ToolResultsSet{ID: "first"}))
				// Bypass preflight so both signals are delivered in exact queue order.
				second, err := rt.storeProvidedResults(ctx, rb7Answer("second"))
				require.NoError(t, err)
				require.NoError(t, eng.SignalByID(ctx, "root", "", api.SignalProvideToolResults, second))
				out, err := handle.Wait(ctx)
				require.NoError(t, err)
				rb8AssertCompleted(t, rt, eng, bus, out)
			})
		}
	}
}

func TestProvideToolResultsConcurrentDuplicatesComplete(t *testing.T) {
	rt, eng, bus, ctx := rb8Runtime(t, "external", false)
	eng.queryGate = make(chan struct{})
	handle, err := rt.MustClient("content.agent").Start(ctx, "session", nil, WithRunID("root"))
	require.NoError(t, err)
	rb8Wait(t, ctx, bus.paused)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			errs <- rt.ProvideToolResults(ctx, rb7Answer("first"))
		}()
	}
	require.NoError(t, <-errs)
	close(bus.release)
	require.NoError(t, <-errs)
	assert.Equal(t, int32(2), eng.signals.Load(), "force the non-atomic query/signal race")
	rb8WaitSecondAwait(t, ctx, eng)
	require.NoError(t, rt.ProvideToolResults(ctx, rb7Answer("second")))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	rb8AssertCompleted(t, rt, eng, bus, out)
}

func TestProvidedToolResultsUnknownSignalStillFails(t *testing.T) {
	rt, eng, bus, ctx := rb8Runtime(t, "external", false)
	handle, err := rt.MustClient("content.agent").Start(ctx, "session", nil, WithRunID("root"))
	require.NoError(t, err)
	rb8Wait(t, ctx, bus.paused)
	require.NoError(t, eng.SignalByID(ctx, "root", "", api.SignalProvideToolResults, &api.ToolResultsSet{ID: "unknown"}))
	close(bus.release)
	_, err = handle.Wait(ctx)
	assertToolResultContractError(t, err)
	require.ErrorContains(t, err, "unexpected await ID")
	assert.Zero(t, eng.records.Load())
}

func (e *rb8Engine) QueryToolAwaits(ctx context.Context, id string) ([]planner.AwaitExternalTools, error) {
	awaits, err := e.Engine.(engine.ToolAwaitQuerier).QueryToolAwaits(ctx, id)
	if e.queryGate != nil {
		n := e.queries.Add(1)
		if n == 2 {
			close(e.queryGate)
		}
		if n <= 2 {
			select {
			case <-e.queryGate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return awaits, err
}

func (e *rb8Engine) SignalByID(ctx context.Context, id, runID, name string, payload any) error {
	e.signals.Add(1)
	return e.Engine.(engine.Signaler).SignalByID(ctx, id, runID, name, payload)
}

func (e *rb8Engine) RegisterRecordContentActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error)) error {
	return e.Engine.RegisterRecordContentActivity(ctx, name, opts, func(ctx context.Context, in *api.RecordContentInput) (*api.RecordContentOutput, error) {
		if in.Provided != nil && e.records.Add(1) == 1 && e.taken != nil {
			close(e.taken)
			select {
			case <-e.recordGate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return fn(ctx, in)
	})
}

func (b *rb8Hooks) Publish(ctx context.Context, event hooks.Event) error {
	if err := b.recordingHooks.Publish(ctx, event); err != nil {
		return err
	}
	if event.Type() == hooks.RunPaused {
		b.once.Do(func() {
			close(b.paused)
		})
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func rb8Runtime(t *testing.T, kind string, acrossTurns bool) (*Runtime, *rb8Engine, *rb8Hooks, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	eng := &rb8Engine{Engine: engineinmem.New()}
	bus := &rb8Hooks{recordingHooks: &recordingHooks{ch: make(chan hooks.Event, 256)}, paused: make(chan struct{}), release: make(chan struct{})}
	rt := New(WithEngine(eng), WithHooks(bus), WithRunStore(&contentPathStore{Store: storeinmem.New()}))
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{providedValidationSpec()}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		panic("external tool must not execute")
	}}))
	resumes := 0
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		items := []planner.AwaitItem{rb7Await(kind, "first")}
		if !acrossTurns {
			items = append(items, rb7Await(kind, "second"))
		}
		return &planner.PlanResult{Await: planner.NewAwait(items...)}, nil
	}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
		resumes++
		if acrossTurns && resumes == 1 {
			return &planner.PlanResult{Await: planner.NewAwait(rb7Await(kind, "second"))}, nil
		}
		var sequence []string
		for _, message := range in.Messages {
			for _, part := range message.Parts {
				switch p := part.(type) {
				case model.ToolUsePart:
					sequence = append(sequence, "use:"+p.ID)
				case model.ToolResultPart:
					sequence = append(sequence, "result:"+p.ToolUseID)
				}
			}
		}
		assert.Equal(t, []string{"use:first", "result:first", "use:second", "result:second"}, sequence)
		return oracleFinal(), nil
	}})
	return rt, eng, bus, ctx
}

func rb8Wait(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func rb8WaitSecondAwait(t *testing.T, ctx context.Context, eng *rb8Engine) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		awaits, err := eng.QueryToolAwaits(ctx, "root")
		assert.NoError(c, err)
		if assert.Len(c, awaits, 1) {
			assert.Equal(c, "second", awaits[0].ID)
		}
	}, time.Second, time.Millisecond)
}

func rb8AssertCompleted(t *testing.T, rt *Runtime, eng *rb8Engine, bus *rb8Hooks, out *RunOutput) {
	t.Helper()
	require.NotNil(t, out)
	assert.Equal(t, contentMessage("done"), out.Final)
	assert.Equal(t, int32(2), eng.records.Load(), "one content activity per accepted await")
	events, err := LoadRunToolEvents(t.Context(), rt.RunStore, "root", out.ToolEventsRef)
	require.NoError(t, err)
	assert.Len(t, events, 2)
	bus.mu.Lock()
	defer bus.mu.Unlock()
	count := 0
	for _, event := range bus.events {
		if event.Type() == hooks.ToolResultReceived {
			count++
		}
	}
	assert.Equal(t, 2, count, "duplicates must not record result events")
}
