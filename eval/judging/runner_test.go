package judging_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type timeoutClient struct {
	model.Client
	blocked atomic.Int32
}

func TestRunnerJudgeTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scripted, script := agenttest.ScriptClient(t, turn("a"))
		client := &timeoutClient{Client: scripted}
		j := adapter(t, client)
		cfg := eval.Config{Judge: j, JudgeTimeout: 50 * time.Millisecond, Harness: eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
			return nil
		}, nil), Scenarios: []eval.Scenario{{ID: "timeout", Items: eval.Items(eval.Item{ID: "slow", Input: "wait"}, eval.Item{ID: "fast", Input: "go"}), Claims: []eval.Claim{{ID: "a", Text: "a", Subject: eval.SubjectAnswer}}, Drive: func(_ context.Context, _ *agenttest.Harness, item eval.Item) (*agenttest.SessionResult, error) {
			out := outcome()
			if item.ID == "slow" {
				out.Session.Trajectory[1].Final = &model.Message{Parts: []model.Part{model.TextPart{Text: "BLOCK"}}}
			}
			return out.Session, nil
		}}}}
		report, err := eval.Run(t.Context(), cfg)
		require.NoError(t, err)
		assert.Equal(t, int32(1), client.blocked.Load())
		assert.Equal(t, eval.VerdictError, report.Attempts[0].Verdict())
		require.ErrorIs(t, report.Attempts[0].Outcome.Judgments[0].Err, context.DeadlineExceeded)
		assert.Equal(t, eval.VerdictPassed, report.Attempts[1].Verdict())
		assert.Len(t, script.Requests(), 1)
		assert.Less(t, report.Attempts[0].Duration, 50*time.Millisecond)
	})
}

func (c *timeoutClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	if strings.Contains(requestText(req), "BLOCK") {
		c.blocked.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.Client.Complete(ctx, req)
}
