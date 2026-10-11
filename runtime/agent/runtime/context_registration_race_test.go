package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/stretchr/testify/require"
)

type contextRegistrationGate struct {
	engine.Engine
	ready   chan struct{}
	release chan struct{}
}

func TestContextRegistrationConcurrentStart(t *testing.T) {
	const registrations = 16
	const starts = 8
	eng := &contextRegistrationGate{Engine: inmem.New(), ready: make(chan struct{}, registrations), release: make(chan struct{})}
	rt := New(WithEngine(eng), WithContextCompaction("agent", ContextCompaction{}), WithContextRecovery("agent", ContextRecovery{}))
	reg := contextRegistration(rt)
	reg.Planner = &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return assistantCommitResult(), nil
	}}
	require.NoError(t, rt.RegisterAgent(t.Context(), reg))
	client, err := rt.Client("agent")
	require.NoError(t, err)
	registered := make(chan error, registrations)
	for i := range registrations {
		pending := contextRegistration(rt)
		pending.ID = agent.Ident(fmt.Sprintf("pending-%d", i))
		pending.Workflow.Name = string(pending.ID)
		pending.PlanActivityName = string(pending.ID) + ".plan"
		pending.ResumeActivityName = string(pending.ID) + ".resume"
		pending.ExecuteToolActivity = string(pending.ID) + ".tool"
		pending.Policy = RunPolicy{ContextCompaction: &ContextCompaction{}, ContextRecovery: &ContextRecovery{DisableRecall: true}}
		go func() {
			registered <- rt.RegisterAgent(t.Context(), pending)
		}()
	}
	for range registrations {
		<-eng.ready
	}
	completed := make(chan error, starts)
	for i := range starts {
		go func() {
			<-eng.release
			_, err := client.OneShotRun(t.Context(), nil, WithRunID(fmt.Sprintf("concurrent-context-%d", i)))
			completed <- err
		}()
	}
	close(eng.release)
	for range registrations {
		require.NoError(t, <-registered)
	}
	for range starts {
		require.NoError(t, <-completed)
	}
}

// RegisterFinishRunActivity holds admitted registrations immediately before
// the runtime publishes their settings, so run starts overlap publication.
func (e *contextRegistrationGate) RegisterFinishRunActivity(ctx context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error)) error {
	if err := e.Engine.RegisterFinishRunActivity(ctx, name, opts, fn); err != nil {
		return err
	}
	if name != FinishRunActivityName("agent") {
		e.ready <- struct{}{}
		<-e.release
	}
	return nil
}
