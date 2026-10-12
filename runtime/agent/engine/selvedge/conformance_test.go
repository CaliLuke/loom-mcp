package selvedge

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/enginetest"
	sv "github.com/CaliLuke/selvedge"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConformance(t *testing.T) {
	enginetest.RunConformance(t, conformanceFactory(func(t *testing.T) string {
		return filepath.Join(t.TempDir(), "s.db")
	}))
}

func conformanceFactory(database func(*testing.T) string) enginetest.Factory {
	return enginetest.Factory{Durable: true, Retries: true, New: func(t *testing.T) enginetest.Fixture {
		db := database(t)
		client := testApp(t, db)
		require.NoError(t, client.Start(t.Context()))
		t.Cleanup(func() {
			assert.NoError(t, client.Shutdown(context.Background()))
		})
		newWorker := func() *Engine {
			e, err := NewWorker(Options{App: testApp(t, db), Client: client})
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, e.Close())
			})
			return e
		}
		current := newWorker()
		queue := "selvedge-conformance-" + uuid.NewString()
		return enginetest.Fixture{Engine: current, TaskQueue: queue, ActivityQueue: queue + "-activities", Restart: func(t *testing.T, register func(engine.Engine)) engine.Engine {
			require.NoError(t, current.Close())
			current = newWorker()
			register(current)
			require.NoError(t, current.SealRegistration(t.Context()))
			return current
		}, VerifyActivityOptions: func(t *testing.T, id, name string, want []engine.ActivityOptions) error {
			return verifyActivityOptions(t, client, id, name, want)
		}}
	}}
}

func verifyActivityOptions(t *testing.T, client *sv.App, id, name string, want []engine.ActivityOptions) error {
	t.Helper()
	tasks, err := sv.ListActivityTasks(t.Context(), client, id)
	if err != nil {
		return err
	}
	got := make([]engine.ActivityOptions, 0, len(want))
	for _, task := range tasks {
		if task.Name != name {
			continue
		}
		p := task.Options.RetryPolicy
		got = append(got, engine.ActivityOptions{Queue: task.Queue, StartToCloseTimeout: task.Options.StartToCloseTimeout, ScheduleToStartTimeout: task.Options.ScheduleToStartTimeout, HeartbeatTimeout: task.Options.HeartbeatTimeout, RetryPolicy: engine.RetryPolicy{MaxAttempts: p.MaxAttempts, InitialInterval: p.InitialInterval, BackoffCoefficient: p.Multiplier}})
	}
	if !assert.ObjectsAreEqual(want, got) {
		return fmt.Errorf("recorded activity options: want %#v, got %#v", want, got)
	}
	return nil
}
