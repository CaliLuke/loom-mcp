package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type payloadMeasurement struct{ bytes, fixed, bound, entries, calls int }
type payloadRecordingEngine struct {
	engine.Engine
	measurements []payloadMeasurement
}

// P2 covers planner envelopes, not retained hook bodies (K8). Keep event
// metadata and a digest in this test's in-memory run log so 50,000 full result
// events do not retain another 5 GiB beside the real in-memory content store.
// Hooks still encode and publish their full payloads through the normal path.
type payloadRunLog struct{ runlog.Store }

func (s payloadRunLog) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	copy := *event
	copy.Payload = rawjson.Message(fmt.Sprintf("\"%x\"", sha256.Sum256(event.Payload)))
	return s.Store.Append(ctx, &copy)
}

// Capture every whole input at the engine boundary, without retaining histories
// or content in the test harness. These scripted planners call no model (M=0).
func (e *payloadRecordingEngine) RegisterPlannerActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.PlanActivityInput) (*api.PlanActivityOutput, error)) error {
	return e.Engine.RegisterPlannerActivity(ctx, name, opts, func(ctx context.Context, in *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		fixed := *in
		fixed.View.Pending = nil
		fixedData, err := json.Marshal(fixed)
		if err != nil {
			return nil, err
		}
		calls := 0
		for _, entry := range in.View.Pending {
			calls += len(entry.Calls)
		}
		e.measurements = append(e.measurements, payloadMeasurement{bytes: len(data), fixed: len(fixedData), bound: 4096 + 400*len(in.View.Pending) + 100*calls, entries: len(in.View.Pending), calls: calls})
		m := e.measurements[len(e.measurements)-1]
		if m.bytes > m.bound {
			return nil, fmt.Errorf("P2 input %d exceeds bound: bytes=%d bound=%d fixed=%d entries=%d calls=%d", len(e.measurements), m.bytes, m.bound, m.fixed, m.entries, m.calls)
		}
		return fn(ctx, in)
	})
}

// TestIndexPayloadAcceptance exercises the full runtime, in-memory engine and
// store. make test-payload-growth (also invoked by make itest) runs the full
// 5 GiB tool workload. The typed workload also runs with -short under race.
func TestIndexPayloadAcceptance(t *testing.T) {
	t.Run("1000_steps_50_parallel_100KB_results", func(t *testing.T) {
		if testing.Short() {
			t.Skip("full payload acceptance: make test-payload-growth (included in make itest)")
		}
		eng := &payloadRecordingEngine{Engine: engineinmem.New()}
		rt := New(WithEngine(eng), WithRunEventStore(payloadRunLog{Store: runloginmem.New()}))
		spec := newAnyJSONSpec("test.large", "test")
		// Identical content deliberately deduplicates storage, but every call still
		// produces, stores, projects and exposes a real 100 KB result.
		payload := strings.Repeat("x", 100*1024)
		require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: payload}), nil
		}}))
		const steps, batch = 1000, 50
		step := 0
		next := func() *planner.PlanResult {
			calls := make([]planner.ToolRequest, batch)
			for i := range calls {
				calls[i] = planner.ToolRequest{Name: spec.Name, ToolCallID: fmt.Sprintf("s%d-c%d", step, i), Payload: rawjson.Message(`{}`)}
			}
			return &planner.PlanResult{ToolCalls: calls}
		}
		registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) { return next(), nil }, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
			step++
			if step%100 == 0 {
				t.Logf("completed %d tool steps", step)
			}
			require.Len(t, in.ToolOutputs, step*batch)
			for _, out := range in.ToolOutputs[len(in.ToolOutputs)-batch:] {
				assert.Empty(t, out.Result)
				assert.Empty(t, out.ServerData)
				loaded, err := in.Content.LoadToolResult(ctx, out.Refs)
				require.NoError(t, err)
				require.Len(t, loaded, len(payload)+2)
			}
			if step == steps {
				return oracleFinal(), nil
			}
			return next(), nil
		}})
		_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("seed")}, WithRunID("root"), WithTurnID("turn"))
		require.NoError(t, err)
		require.Equal(t, steps, step)
		assertPayloadMeasurements(t, eng, steps+1)
	})
	t.Run("500_typed_await_rounds_10KB", func(t *testing.T) {
		eng := &payloadRecordingEngine{Engine: engineinmem.New()}
		bus := &recordingHooks{ch: make(chan hooks.Event, 128)}
		rt := New(WithEngine(eng), WithHooks(bus))
		const rounds = 500
		round := 0
		payload := rawjson.Message(`{"answer":"` + strings.Repeat("a", 10*1024-len(`{"answer":""}`)) + `"}`)
		next := func() *planner.PlanResult {
			return &planner.PlanResult{Await: planner.NewAwait(planner.AwaitTypedInputItem(&planner.AwaitTypedInput{ID: fmt.Sprintf("answer-%d", round), Schema: rawjson.Message(`{"type":"object"}`)}))}
		}
		registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) { return next(), nil }, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
			round++
			require.Len(t, in.TypedInputs, round)
			latest := in.TypedInputs[round-1]
			assert.Empty(t, latest.Payload)
			data, err := in.Content.LoadTypedInput(ctx, latest.PayloadRef)
			require.NoError(t, err)
			assert.Equal(t, payload, data)
			if round == rounds {
				return oracleFinal(), nil
			}
			return next(), nil
		}})
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		h, err := rt.MustClient("content.agent").Start(ctx, "session", nil, WithRunID("root"), WithTurnID("turn"))
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { _, err := h.Wait(ctx); done <- err }()
		for {
			select {
			case err := <-done:
				require.NoError(t, err)
				require.Equal(t, rounds, round)
				assertPayloadMeasurements(t, eng, rounds+1)
				return
			case ev := <-bus.ch:
				if ev, ok := ev.(*hooks.AwaitTypedInputEvent); ok {
					require.NoError(t, rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{RunID: "root", ID: ev.ID, Payload: payload}))
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	})
}

func assertPayloadMeasurements(t *testing.T, e *payloadRecordingEngine, count int) {
	t.Helper()
	require.Len(t, e.measurements, count)
	maxBytes, maxFixed := 0, 0
	for i, m := range e.measurements {
		require.LessOrEqual(t, m.bytes, m.bound, "input %d: entries=%d calls=%d fixed=%d", i, m.entries, m.calls, m.fixed)
		require.LessOrEqual(t, m.fixed, 4096, "fixed input %d", i)
		maxBytes = max(maxBytes, m.bytes)
		maxFixed = max(maxFixed, m.fixed)
	}
	t.Logf("P2 inputs=%d max_input=%d fixed_max=%d bytes models=0", count, maxBytes, maxFixed)
}
