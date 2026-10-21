package runtime

import (
	"context"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rejectingStartupStore struct {
	runstore.Store
}

func TestStartupInputReachesPlanner(t *testing.T) {
	for _, sessionful := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			text string
		}{
			{"inline", strings.Repeat("x", 900_000)},
			{"oversized", strings.Repeat("x", 1_100_000)},
			{"escaped", strings.Repeat("\x00", 200_000)},
		} {
			t.Run(fmt.Sprintf("%s/session=%v", tc.name, sessionful), func(t *testing.T) {
				bus := hooks.NewBus()
				var started *hooks.RunStartedEvent
				sub, err := bus.Register(hooks.SubscriberFunc(func(_ context.Context, event hooks.Event) error {
					if event, ok := event.(*hooks.RunStartedEvent); ok {
						started = event
					}
					return nil
				}))
				require.NoError(t, err)
				t.Cleanup(func() {
					assert.NoError(t, sub.Close())
				})
				// The stored hook input must include the interceptor's effective input.
				var effective RunInput
				rt := New(WithEngine(engineinmem.New()), WithHooks(bus), WithInterceptors(RuntimeInterceptorFuncs{
					BeforeRunFunc: func(_ context.Context, in *BeforeRunInput) (*BeforeRunDecision, error) {
						effective = in.Input
						effective.Messages = []*model.Message{contentMessage(tc.text)}
						return &BeforeRunDecision{Input: &effective}, nil
					},
				}))
				var received []*model.Message
				registerContentAgent(t, rt, &stubPlanner{start: func(_ context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
					received = in.Messages
					return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("done")}}, nil
				}})
				messages := []*model.Message{contentMessage("before interception")}
				if sessionful {
					var handle engine.WorkflowHandle
					handle, err = rt.MustClient("content.agent").Start(t.Context(), "session", messages, WithRunTimeBudget(time.Minute))
					if err == nil {
						_, err = handle.Wait(t.Context())
					}
				} else {
					_, err = rt.MustClient("content.agent").OneShotRun(t.Context(), messages, WithRunTimeBudget(time.Minute))
				}
				require.NoError(t, err)
				assert.Equal(t, effective.Messages, received)
				require.NotNil(t, started)
				encoded, err := hooks.EncodeToHookInput(started, started.TurnID())
				require.NoError(t, err)
				assert.LessOrEqual(t, len(encoded.Payload), maxHookPayloadBytes)
				want, err := json.Marshal(effective, json.FormatNilMapAsNull(true), json.FormatNilSliceAsNull(true), jsonopts.FormatDurationAsNano(true))
				require.NoError(t, err)
				if tc.name == "inline" {
					assert.Empty(t, started.InputRef)
					got, err := json.Marshal(started.Input)
					require.NoError(t, err)
					assert.JSONEq(t, string(want), string(got))
					return
				}
				assert.Nil(t, started.Input)
				require.NotEmpty(t, started.InputRef)
				got, err := rt.RunStore.Get(t.Context(), started.RunContext.RootRunID, started.InputRef)
				require.NoError(t, err)
				assert.JSONEq(t, string(want), string(got))
			})
		}
	}
}

func TestStartupInputStoreFailureStopsPlanning(t *testing.T) {
	rt := New(WithEngine(engineinmem.New()), WithRunStore(rejectingStartupStore{Store: storeinmem.New()}))
	called := false
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		called = true
		return nil, fmt.Errorf("planner must not run")
	}})
	_, err := rt.MustClient("content.agent").OneShotRun(t.Context(), []*model.Message{contentMessage(strings.Repeat("x", 1_100_000))})
	require.ErrorIs(t, err, runstore.ErrContentTooLarge)
	assert.False(t, called)
}

func (s rejectingStartupStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if len(data) > maxHookPayloadBytes {
		return "", runstore.ErrContentTooLarge
	}
	return s.Store.Put(ctx, scope, data)
}
