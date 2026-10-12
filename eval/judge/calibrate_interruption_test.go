package judge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type calibrationGateClient struct {
	model.Client
	once    sync.Once
	release chan struct{}
	failure error
}

func TestCalibrationWaitersOwnCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		script, err := agenttest.NewScript(calibrationTurns()...)
		require.NoError(t, err)
		client := &calibrationGateClient{Client: rawClient{script: script}, release: make(chan struct{})}
		j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: NewMemoryCache()})
		require.NoError(t, err)
		firstCtx, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		var firstErr, liveErr, expiredErr error
		var liveResult *Calibration
		go func() {
			_, firstErr = j.Calibrate(firstCtx)
		}()
		synctest.Wait()
		expiredCtx, cancelExpired := context.WithTimeout(t.Context(), time.Second)
		defer cancelExpired()
		go func() {
			_, expiredErr = j.Calibrate(expiredCtx)
		}()
		go func() {
			liveResult, liveErr = j.Calibrate(t.Context())
		}()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		require.ErrorIs(t, expiredErr, context.DeadlineExceeded)
		cancelFirst()
		close(client.release)
		synctest.Wait()
		require.ErrorIs(t, firstErr, context.Canceled)
		require.NoError(t, liveErr, "an active waiter must not inherit the initiating caller's cancellation")
		require.NotNil(t, liveResult)
		assert.True(t, liveResult.Passed)
		assert.Len(t, script.Requests(), 4)
	})
}

func TestCalibrationProviderFailureDuringCancellationRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		script, err := agenttest.NewScript(calibrationTurns()...)
		require.NoError(t, err)
		client := &calibrationGateClient{Client: rawClient{script: script}, release: make(chan struct{}), failure: errors.New("provider disconnected")}
		j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: NewMemoryCache()})
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var firstErr error
		go func() {
			_, firstErr = j.Calibrate(ctx)
		}()
		synctest.Wait()
		cancel()
		close(client.release)
		synctest.Wait()
		require.ErrorIs(t, firstErr, context.Canceled)
		result, err := j.Calibrate(t.Context())
		require.NoError(t, err)
		assert.True(t, result.Passed)
		assert.Len(t, script.Requests(), 4)
	})
}

func TestCalibrationCompletedAttemptHonorsWaiterContext(t *testing.T) {
	j := newTestJudge(t, unusedClient{})
	_, err := j.Calibrate(t.Context())
	require.NoError(t, err)
	// Both the shared completion and this context are ready. Context ownership
	// must hold regardless of which select case would otherwise win.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 20 {
		_, err := j.Calibrate(ctx)
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func (c *calibrationGateClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	first := false
	c.once.Do(func() {
		first = true
	})
	if first {
		<-c.release
		if c.failure != nil {
			return nil, c.failure
		}
		return nil, ctx.Err()
	}
	return c.Client.Complete(ctx, req)
}
