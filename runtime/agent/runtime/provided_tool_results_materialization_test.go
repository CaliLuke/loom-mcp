package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvideToolResultsMaterializesBoundsBeforeValidation(t *testing.T) {
	for _, kind := range []string{"questions", "external"} {
		t.Run(kind, func(t *testing.T) {
			bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
			rt := New(WithEngine(engineinmem.New()), WithHooks(bus))
			spec := providedValidationSpec()
			spec.Bounds = &tools.BoundsSpec{}
			var materializations atomic.Int32
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
				panic("external tool must not execute")
			}, ResultMaterializer: func(_ context.Context, _ ToolCallMeta, call *planner.ToolRequest, result *planner.ToolResult) error {
				if string(call.Payload) != `{"limit":1}` {
					return fmt.Errorf("materializer requires awaited call payload, got %s", call.Payload)
				}
				materializations.Add(1)
				result.Bounds = &agent.Bounds{Returned: 1}
				result.RetryHint.Message += "enriched"
				return nil
			}}))
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{Await: planner.NewAwait(rb7Await(kind, "await"))}, nil
			}, resume: func(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
				require.Len(t, in.ToolOutputs, 1)
				assert.Equal(t, &agent.Bounds{Returned: 1}, in.ToolOutputs[0].Bounds)
				assert.Equal(t, "enriched", in.ToolOutputs[0].RetryHint.Message)
				assert.Equal(t, int32(2), materializations.Load(), "preflight and activity must each materialize raw input")
				return oracleFinal(), nil
			}})
			runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
				if event.Type() != hooks.RunPaused {
					return nil
				}
				answer := rb7Answer("await")
				answer.Results[0].RetryHint = &planner.RetryHint{}
				require.NoError(t, rt.ProvideToolResults(t.Context(), answer))
				assert.Nil(t, answer.Results[0].Bounds, "preflight enrichment must not replace caller input")
				assert.Empty(t, answer.Results[0].RetryHint.Message, "preflight must not mutate caller metadata")
				return nil
			})
		})
	}
}

func TestProvideToolResultsRejectsMaterializedContractViolation(t *testing.T) {
	bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithEngine(engineinmem.New()), WithRunStore(store), WithHooks(bus))
	spec := providedValidationSpec()
	var invalid atomic.Bool
	invalid.Store(true)
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		panic("external tool must not execute")
	}, ResultMaterializer: func(_ context.Context, _ ToolCallMeta, _ *planner.ToolRequest, result *planner.ToolResult) error {
		if invalid.Load() {
			result.Bounds = &agent.Bounds{}
		}
		return nil
	}}))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{Await: planner.NewAwait(rb7Await("external", "await"))}, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}})
	runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
		if event.Type() != hooks.RunPaused {
			return nil
		}
		answer := rb7Answer("await")
		before := store.snapshot()
		err := rt.ProvideToolResults(t.Context(), answer)
		require.Error(t, err, "preflight must reject invalid materialized bounds")
		assertToolResultContractError(t, err)
		assert.Equal(t, before, store.snapshot())
		_, _, err = rt.decodeProvidedToolResults(t.Context(), []planner.ToolRequest{{Name: spec.Name, ToolCallID: "await"}}, map[string]*api.ProvidedToolResult{"await": answer.Results[0]})
		assertToolResultContractError(t, err)
		invalid.Store(false)
		return rt.ProvideToolResults(t.Context(), answer)
	})
}

func TestProvideToolResultsClassifiesMaterializerError(t *testing.T) {
	bus := &recordingHooks{ch: make(chan hooks.Event, 256)}
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithEngine(engineinmem.New()), WithRunStore(store), WithHooks(bus))
	spec := providedValidationSpec()
	failure := errors.New("invalid enrichment")
	var invalid atomic.Bool
	invalid.Store(true)
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
		panic("external tool must not execute")
	}, ResultMaterializer: func(context.Context, ToolCallMeta, *planner.ToolRequest, *planner.ToolResult) error {
		if invalid.Load() {
			return failure
		}
		return nil
	}}))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return &planner.PlanResult{Await: planner.NewAwait(rb7Await("external", "await"))}, nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return oracleFinal(), nil
	}})
	runIndexOracle(t, rt, bus, nil, func(event hooks.Event) error {
		if event.Type() != hooks.RunPaused {
			return nil
		}
		answer := rb7Answer("await")
		before := store.snapshot()
		err := rt.ProvideToolResults(t.Context(), answer)
		require.ErrorIs(t, err, failure)
		assertToolResultContractError(t, err)
		assert.Equal(t, before, store.snapshot())
		_, _, err = rt.decodeProvidedToolResults(t.Context(), []planner.ToolRequest{{Name: spec.Name, ToolCallID: "await"}}, map[string]*api.ProvidedToolResult{"await": answer.Results[0]})
		assertToolResultContractError(t, err)
		invalid.Store(false)
		return rt.ProvideToolResults(t.Context(), answer)
	})
}
