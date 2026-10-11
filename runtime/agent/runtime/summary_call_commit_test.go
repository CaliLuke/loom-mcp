package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summaryCrashClient struct {
	model.Client
	crash bool
}
type summaryZombieClient struct {
	model.Client
	sent, release chan struct{}
}

func TestSummaryCallCommitCrashWindows(t *testing.T) {
	for _, test := range []struct {
		window string
		class  runrecord.CallClass
		sent   int
		adopt  bool
	}{
		{"before_started", "", 0, false},
		{"after_started_before_send", runrecord.Incomplete, 0, false},
		{"after_send", runrecord.Incomplete, 1, false},
		{"after_completion_before_return", runrecord.Abandoned, 1, false},
		{"return_lost", runrecord.Abandoned, 1, false},
		{"before_adoption", runrecord.Abandoned, 1, false},
		{"after_adoption", runrecord.Adopted, 1, true},
	} {
		t.Run(test.window, func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			rt.RunStore = crashCallStore{Store: rt.RunStore, window: test.window}
			client.store = rt.RunStore
			raw := &summaryCrashClient{Client: client, crash: test.window == "after_send"}
			require.NoError(t, rt.RegisterModel("summary", raw))
			runUntilCrash(func() error {
				out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
				if err != nil {
					return err
				}
				if test.window == "return_lost" || test.window == "before_adoption" {
					return errors.New("lost activity output")
				}
				event := hooks.NewContextCompactionEvent("run", "agent", "", 9, hooks.ContextCompacted, "", "summary", 1, 1, 100, 50, out.Checkpoint, time.Unix(1, 0))
				event.CallKeys = out.CallKeys
				input, err := hooks.EncodeToHookInput(event, "")
				if err != nil {
					return err
				}
				if err := rt.hookActivity(t.Context(), input); err != nil {
					return err
				}
				if test.window == "after_adoption" {
					panic("engine crashed after adoption")
				}
				return nil
			})
			assert.Len(t, client.requests, test.sent, "SentImpliesRecorded")
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			if test.class == "" {
				assert.Empty(t, record.Runs[0].Calls)
			} else {
				require.Len(t, record.Runs[0].Calls, 1)
				assert.Equal(t, test.class, record.Runs[0].Calls[0].Class, "ClassificationCorrect")
				assert.Equal(t, callrecord.ActivitySummarize, record.Runs[0].Calls[0].Activity)
			}
		})
	}
}

func TestSummaryZombieAttemptAbandoned(t *testing.T) {
	rt, in, client, _ := summaryFixture(t)
	delayed := &summaryZombieClient{Client: client, sent: make(chan struct{}), release: make(chan struct{})}
	require.NoError(t, rt.RegisterModel("summary", delayed))
	ctx, cancel := context.WithCancel(engine.WithActivityAttempt(t.Context(), 1))
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := rt.SummarizeContextActivity(ctx, in); done <- err }()
	<-delayed.sent
	cancel()
	// A fresh registered test client allows the next attempt to finish while
	// the canceled provider is still running; its later completion cannot adopt itself.
	fresh := &summaryTestClient{store: rt.RunStore, limits: client.limits, response: client.response}
	require.NoError(t, rt.RegisterModel("summary", fresh))
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 2), in)
	require.NoError(t, err)
	require.NotNil(t, out.Checkpoint)
	event := hooks.NewContextCompactionEvent("run", "agent", "", 9, hooks.ContextCompacted, "", "summary", 1, 1, 100, 50, out.Checkpoint, time.Unix(1, 0))
	event.CallKeys = out.CallKeys
	hook, err := hooks.EncodeToHookInput(event, "")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(t.Context(), hook))
	close(delayed.release)
	require.ErrorIs(t, <-done, context.Canceled)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs[0].Calls, 2)
	assert.Equal(t, runrecord.Abandoned, record.Runs[0].Calls[0].Class)
	assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[1].Class)
	assert.NotEqual(t, record.Runs[0].Calls[0].Record.Key, record.Runs[0].Calls[1].Record.Key)
}

func (c *summaryCrashClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	resp, err := c.Client.Complete(ctx, req)
	if c.crash {
		panic("worker disappeared after send")
	}
	return resp, err
}
func (c *summaryCrashClient) ContextLimits(ctx context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	return c.Client.(model.ContextInfo).ContextLimits(ctx, req)
}
func (c *summaryZombieClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	resp, err := c.Client.Complete(ctx, req)
	close(c.sent)
	<-c.release
	return resp, err
}
func (c *summaryZombieClient) ContextLimits(ctx context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	return c.Client.(model.ContextInfo).ContextLimits(ctx, req)
}
