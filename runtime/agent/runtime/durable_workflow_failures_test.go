package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type durableWorkflowStore struct {
	runstore.Store
	mu         sync.Mutex
	operations []string
	failAt     int
}

type durableWorkflowLog struct {
	runlog.Store
	mu       sync.Mutex
	kinds    []hooks.EventType
	reject   hooks.EventType
	rejected bool
}

func TestWorkflowCannotSucceedAfterDurableContentFailure(t *testing.T) {
	for _, withTool := range []bool{false, true} {
		t.Run(fmt.Sprint(withTool), func(t *testing.T) {
			baseline, _, err := runDurableFailureScenario(t, withTool, 0, "")
			require.NoError(t, err)
			require.NotEmpty(t, baseline.operations)
			for position, operation := range baseline.operations {
				t.Run(fmt.Sprintf("%02d_%s", position+1, operation), func(t *testing.T) {
					_, _, err := runDurableFailureScenario(t, withTool, position+1, "")
					require.ErrorIs(t, err, assert.AnError, "a durable content failure must not produce a successful run")
				})
			}
		})
	}
}

func TestWorkflowCanonicalEventFailureContract(t *testing.T) {
	_, baseline, err := runDurableFailureScenario(t, true, 0, "")
	require.NoError(t, err)
	seen := map[hooks.EventType]bool{}
	for _, kind := range baseline.kinds {
		if seen[kind] {
			continue
		}
		seen[kind] = true
		t.Run(string(kind), func(t *testing.T) {
			_, log, err := runDurableFailureScenario(t, true, 0, kind)
			assert.True(t, log.rejected)
			if kind == hooks.RunCompleted {
				// Terminal engine state remains authoritative; the completion
				// projection is repaired by subsequent run reads.
				require.NoError(t, err)
				page, readErr := log.Store.List(t.Context(), "root", "", 100)
				require.NoError(t, readErr)
				for _, event := range page.Events {
					assert.NotEqual(t, hooks.RunCompleted, event.Type)
				}
				return
			}
			require.ErrorIs(t, err, assert.AnError)
		})
	}
}

func (s *durableWorkflowStore) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	if s.record("get") {
		return nil, assert.AnError
	}
	return s.Store.Get(ctx, scope, hash)
}

func (s *durableWorkflowStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if s.record("put") {
		return "", assert.AnError
	}
	return s.Store.Put(ctx, scope, data)
}

func (s *durableWorkflowLog) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	s.mu.Lock()
	s.kinds = append(s.kinds, event.Type)
	reject := event.Type == s.reject
	s.rejected = s.rejected || reject
	s.mu.Unlock()
	if reject {
		return runlog.AppendResult{}, assert.AnError
	}
	return s.Store.Append(ctx, event)
}

// runDurableFailureScenario drives the public run API through a seeded answer
// or an inline tool turn. Failures occur only at the durable store boundary.
func runDurableFailureScenario(t *testing.T, withTool bool, failAt int, reject hooks.EventType, activity ...bool) (*durableWorkflowStore, *durableWorkflowLog, error) {
	t.Helper()
	rt := New(WithoutContextDefault("content.agent"))
	store := &durableWorkflowStore{Store: rt.RunStore, failAt: failAt}
	log := &durableWorkflowLog{Store: rt.RunEventStore, reject: reject}
	rt.RunStore, rt.RunEventStore = store, log
	spec := newAnyJSONSpec("inline.read", "inline")
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "inline", Inline: len(activity) == 0 || !activity[0], Specs: []tools.ToolSpec{spec}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
		return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"found": true}, ServerData: rawjson.Message(`{"private":true}`)}), nil
	}}))
	registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		if withTool {
			return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}}}, nil
		}
		return assistantCommitResult(), nil
	}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return assistantCommitResult(), nil
	}})
	_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("remember this")}, WithRunID("root"))
	return store, log, err
}

func (s *durableWorkflowStore) record(operation string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations = append(s.operations, operation)
	return len(s.operations) == s.failAt
}
