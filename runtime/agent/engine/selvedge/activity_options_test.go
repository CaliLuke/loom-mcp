package selvedge

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
	"github.com/CaliLuke/selvedge/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type phaseBActivity struct {
	name     string
	register func(*Engine, string, engine.ActivityOptions) error
	invoke   func(engine.WorkflowContext, string, engine.ActivityOptions) error
}

func TestRetryPolicyMapping(t *testing.T) {
	for _, tc := range []struct {
		name             string
		definition, call engine.ActivityOptions
		want             sv.ActivityOptions
	}{
		{name: "complete zero defaults", want: sv.ActivityOptions{StartToCloseTimeout: 2 * time.Minute, RetryPolicy: &retry.Policy{InitialInterval: time.Second, MaxInterval: 100 * time.Second, Multiplier: 2}}},
		{name: "explicit definition", definition: engine.ActivityOptions{Queue: "registered", StartToCloseTimeout: 9 * time.Second, ScheduleToStartTimeout: 8 * time.Second, HeartbeatTimeout: 7 * time.Second, RetryPolicy: engine.RetryPolicy{MaxAttempts: 6, InitialInterval: 3 * time.Millisecond, BackoffCoefficient: 4}}, want: sv.ActivityOptions{Queue: "registered", StartToCloseTimeout: 9 * time.Second, ScheduleToStartTimeout: 8 * time.Second, HeartbeatTimeout: 7 * time.Second, RetryPolicy: &retry.Policy{MaxAttempts: 6, InitialInterval: 3 * time.Millisecond, MaxInterval: 300 * time.Millisecond, Multiplier: 4}}},
		{name: "attempt-only inherits intervals", definition: engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{MaxAttempts: 6, InitialInterval: 3 * time.Millisecond, BackoffCoefficient: 4}}, call: engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{MaxAttempts: 2}}, want: sv.ActivityOptions{StartToCloseTimeout: 2 * time.Minute, RetryPolicy: &retry.Policy{MaxAttempts: 2, InitialInterval: 3 * time.Millisecond, MaxInterval: 300 * time.Millisecond, Multiplier: 4}}},
		{name: "call interval derives maximum", definition: engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{MaxAttempts: 6, InitialInterval: 3 * time.Millisecond, BackoffCoefficient: 4}}, call: engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{InitialInterval: 5 * time.Millisecond}}, want: sv.ActivityOptions{StartToCloseTimeout: 2 * time.Minute, RetryPolicy: &retry.Policy{MaxAttempts: 6, InitialInterval: 5 * time.Millisecond, MaxInterval: 500 * time.Millisecond, Multiplier: 4}}},
		{name: "coefficient-only inherits intervals", definition: engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{InitialInterval: 3 * time.Millisecond}}, call: engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{BackoffCoefficient: 5}}, want: sv.ActivityOptions{StartToCloseTimeout: 2 * time.Minute, RetryPolicy: &retry.Policy{InitialInterval: 3 * time.Millisecond, MaxInterval: 300 * time.Millisecond, Multiplier: 5}}},
		{name: "call fields and heartbeat cap", definition: engine.ActivityOptions{Queue: "registered", ScheduleToStartTimeout: 8 * time.Second}, call: engine.ActivityOptions{Queue: "called", StartToCloseTimeout: 4 * time.Second, ScheduleToStartTimeout: 6 * time.Second, HeartbeatTimeout: 9 * time.Second}, want: sv.ActivityOptions{Queue: "called", StartToCloseTimeout: 4 * time.Second, ScheduleToStartTimeout: 6 * time.Second, HeartbeatTimeout: 4 * time.Second, RetryPolicy: &retry.Policy{InitialInterval: time.Second, MaxInterval: 100 * time.Second, Multiplier: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t)
			require.NoError(t, e.RegisterExecuteToolActivity(t.Context(), "probe", tc.definition, func(context.Context, *api.ToolInput) (*api.ToolOutput, error) {
				return &api.ToolOutput{}, nil
			}))
			require.NoError(t, runWorkflow(t, e, "options", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
				_, err := w.ExecuteToolActivity(w.Context(), engine.ToolActivityCall{Name: "probe", Input: &api.ToolInput{}, Options: tc.call})
				return &api.RunOutput{}, err
			}))
			tasks, err := sv.ListActivityTasks(t.Context(), e.client, "options")
			require.NoError(t, err)
			require.Len(t, tasks, 1)
			assert.Equal(t, tc.want, tasks[0].Options)
		})
	}
}

func TestActivityClassDefaults(t *testing.T) {
	defaults := ActivityDefaults{Hook: ActivityTimeoutDefaults{QueueWait: 11 * time.Second, Liveness: 7 * time.Second}, Planner: ActivityTimeoutDefaults{QueueWait: 12 * time.Second, Liveness: 8 * time.Second}, Tool: ActivityTimeoutDefaults{QueueWait: 13 * time.Second, Liveness: 9 * time.Second}}
	for _, a := range phaseBActivities() {
		t.Run(a.name, func(t *testing.T) {
			e, err := NewWorker(Options{App: testApp(t, filepath.Join(t.TempDir(), "s.db")), ActivityDefaults: defaults})
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, e.Close())
			})
			want := defaults.Hook
			if a.name == "planner" || a.name == "summary" {
				want = defaults.Planner
			}
			if a.name == "tool" {
				want = defaults.Tool
			}
			require.NoError(t, a.register(e, "default", engine.ActivityOptions{}))
			require.NoError(t, a.register(e, "explicit", engine.ActivityOptions{ScheduleToStartTimeout: 20 * time.Second, HeartbeatTimeout: 21 * time.Second}))
			require.NoError(t, runWorkflow(t, e, "classes", func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
				if err := a.invoke(w, "default", engine.ActivityOptions{}); err != nil {
					return nil, err
				}
				if err := a.invoke(w, "explicit", engine.ActivityOptions{}); err != nil {
					return nil, err
				}
				err := a.invoke(w, "explicit", engine.ActivityOptions{ScheduleToStartTimeout: 30 * time.Second, HeartbeatTimeout: 31 * time.Second})
				return &api.RunOutput{}, err
			}))
			tasks, err := sv.ListActivityTasks(t.Context(), e.client, "classes")
			require.NoError(t, err)
			require.Len(t, tasks, 3)
			assert.Equal(t, want.QueueWait, tasks[0].Options.ScheduleToStartTimeout)
			assert.Equal(t, want.Liveness, tasks[0].Options.HeartbeatTimeout)
			assert.Equal(t, 20*time.Second, tasks[1].Options.ScheduleToStartTimeout)
			assert.Equal(t, 21*time.Second, tasks[1].Options.HeartbeatTimeout)
			assert.Equal(t, 30*time.Second, tasks[2].Options.ScheduleToStartTimeout)
			assert.Equal(t, 31*time.Second, tasks[2].Options.HeartbeatTimeout)
		})
	}
}

func phaseBActivities() []phaseBActivity {
	return []phaseBActivity{
		{name: "hook", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterHookActivity(context.Background(), n, o, func(context.Context, *api.HookActivityInput) error {
				return nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			return w.PublishHook(w.Context(), engine.HookActivityCall{Name: n, Input: &api.HookActivityInput{}, Options: o})
		}},
		{name: "planner", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterPlannerActivity(context.Background(), n, o, func(context.Context, *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
				return &api.PlanActivityOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecutePlannerActivity(w.Context(), engine.PlannerActivityCall{Name: n, Input: &api.PlanActivityInput{}, Options: o})
			return err
		}},
		{name: "tool", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterExecuteToolActivity(context.Background(), n, o, func(context.Context, *api.ToolInput) (*api.ToolOutput, error) {
				return &api.ToolOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecuteToolActivity(w.Context(), engine.ToolActivityCall{Name: n, Input: &api.ToolInput{}, Options: o})
			return err
		}},
		{name: "seal", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterSealRecordActivity(context.Background(), n, o, func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error) {
				return &api.SealRecordOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecuteSealRecordActivity(w.Context(), engine.SealRecordActivityCall{Name: n, Input: &api.SealRecordInput{}, Options: o})
			return err
		}},
		{name: "finish", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterFinishRunActivity(context.Background(), n, o, func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error) {
				return &api.FinishRunOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecuteFinishRunActivity(w.Context(), engine.FinishRunActivityCall{Name: n, Input: &api.FinishRunInput{}, Options: o})
			return err
		}},
		{name: "record_content", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterRecordContentActivity(context.Background(), n, o, func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error) {
				return &api.RecordContentOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecuteRecordContentActivity(w.Context(), engine.RecordContentActivityCall{Name: n, Input: &api.RecordContentInput{}, Options: o})
			return err
		}},
		{name: "summary", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterSummarizeContextActivity(context.Background(), n, o, func(context.Context, *api.SummarizeContextInput) (*api.SummarizeContextOutput, error) {
				return &api.SummarizeContextOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecuteSummarizeContextActivity(w.Context(), engine.SummarizeContextActivityCall{Name: n, Input: &api.SummarizeContextInput{}, Options: o})
			return err
		}},
		{name: "recall", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterRecallActivity(context.Background(), n, o, func(context.Context, *api.RecallActivityInput) (*api.RecallActivityOutput, error) {
				return &api.RecallActivityOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecuteRecallActivity(w.Context(), engine.RecallActivityCall{Name: n, Input: &api.RecallActivityInput{}, Options: o})
			return err
		}},
		{name: "child_dispatch", register: func(e *Engine, n string, o engine.ActivityOptions) error {
			return e.RegisterChildDispatchActivity(context.Background(), n, o, func(context.Context, *api.ChildDispatchInput) (*api.ChildDispatchOutput, error) {
				return &api.ChildDispatchOutput{}, nil
			})
		}, invoke: func(w engine.WorkflowContext, n string, o engine.ActivityOptions) error {
			_, err := w.ExecuteChildDispatchActivity(w.Context(), engine.ChildDispatchActivityCall{Name: n, Input: &api.ChildDispatchInput{}, Options: o})
			return err
		}},
	}
}
