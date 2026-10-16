package eval_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
)

func TestInputBuilderValidation(t *testing.T) {
	builder := func(context.Context, eval.Item) ([]agenttest.Input, error) {
		return nil, nil
	}
	for _, tc := range []struct {
		name  string
		drive bool
		agent string
		want  string
	}{
		{name: "conflicting callbacks", drive: true, agent: "agent", want: "BuildInputs"},
		{name: "missing agent", want: "Agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := customScenario(syntheticSession())
			if !tc.drive {
				s.Drive = nil
			}
			s.Agent = ""
			if tc.agent != "" {
				s.Agent = "agent"
			}
			s.BuildInputs = builder
			err := eval.Validate(s)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestInputBuilderOperationalFailures(t *testing.T) {
	failure := errors.New("prompt source unavailable")
	for _, tc := range []struct {
		name    string
		build   func(context.Context, eval.Item) ([]agenttest.Input, error)
		want    error
		message string
	}{
		{name: "builder error", build: func(context.Context, eval.Item) ([]agenttest.Input, error) {
			return nil, failure
		}, want: failure},
		{name: "builder panic", build: func(context.Context, eval.Item) ([]agenttest.Input, error) {
			panic("prompt builder failed")
		}, message: "prompt builder failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := customScenario(syntheticSession())
			s.Drive = nil
			s.Agent = "agent"
			s.BuildInputs = tc.build
			out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
			require.NoError(t, err)
			require.Error(t, out.Err)
			assert.Nil(t, out.Session)
			assert.Empty(t, out.Checks)
			assert.Empty(t, out.Scores)
			if tc.want != nil {
				require.ErrorIs(t, out.Err, tc.want)
			}
			if tc.message != "" {
				require.ErrorContains(t, out.Err, tc.message)
			}
		})
	}
}

func TestInputBuilderOwnsResolvedItem(t *testing.T) {
	stop := errors.New("stop before execution")
	s := customScenario(syntheticSession())
	s.Drive = nil
	s.Agent = "agent"
	s.Task, s.Reference = "scenario task", "scenario reference"
	s.Items = eval.Items(eval.Item{ID: "item", Turns: []string{"original"}, Data: []byte(`{"n":1}`)})
	called := false
	s.BuildInputs = func(_ context.Context, item eval.Item) ([]agenttest.Input, error) {
		called = true
		assert.Equal(t, "scenario task", item.Task)
		assert.Equal(t, "scenario reference", item.Reference)
		item.Turns[0] = "changed"
		item.Data[5] = '2'
		return nil, stop
	}
	out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.ErrorIs(t, out.Err, stop)
	assert.True(t, called)
	assert.Equal(t, []string{"original"}, s.Items.Items()[0].Turns)
	assert.JSONEq(t, `{"n":1}`, string(s.Items.Items()[0].Data))
}

func TestInputBuilderDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := customScenario(syntheticSession())
		s.Drive = nil
		s.Agent = "agent"
		s.Timeout = time.Second
		called := false
		s.BuildInputs = func(ctx context.Context, _ eval.Item) ([]agenttest.Input, error) {
			called = true
			_, deadline := ctx.Deadline()
			assert.True(t, deadline)
			<-ctx.Done()
			return []agenttest.Input{agenttest.User("late input")}, nil
		}
		out, err := eval.Execute(t.Context(), &agenttest.Harness{}, s, s.Items.Items()[0])
		require.NoError(t, err)
		require.ErrorIs(t, out.Err, context.DeadlineExceeded)
		assert.True(t, called)
		assert.Nil(t, out.Session)
		assert.NotContains(t, out.Err.Error(), "panic")
	})
}

func TestInputBuilderCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := customScenario(syntheticSession())
	s.Drive = nil
	s.Agent = "agent"
	called := false
	s.BuildInputs = func(ctx context.Context, _ eval.Item) ([]agenttest.Input, error) {
		called = true
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	out, err := eval.Execute(ctx, &agenttest.Harness{}, s, s.Items.Items()[0])
	require.NoError(t, err)
	require.ErrorIs(t, out.Err, context.Canceled)
	assert.True(t, called)
	assert.Nil(t, out.Session)
}
