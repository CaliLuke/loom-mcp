package judge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingClient struct{ unusedClient }

func TestCalibrationLabelsAndCache(t *testing.T) {
	for _, passed := range []bool{true, false} {
		t.Run(map[bool]string{true: "passed", false: "failed"}[passed], func(t *testing.T) {
			turns := calibrationTurns()
			if !passed {
				turns[0] = calibrationTurn(Contradicted)
			}
			turns = append(turns, validTurn(), validTurn())
			client, script := agenttest.ScriptClient(t, turns...)
			cache := NewMemoryCache()
			cfg := Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: cache}
			j, err := New(cfg)
			require.NoError(t, err)
			result, err := j.Judge(t.Context(), testInput())
			require.NoError(t, err)
			assert.Equal(t, passed, result.Calibrated)
			calibration, err := j.Calibrate(t.Context())
			require.NoError(t, err)
			require.Len(t, calibration.Examples, 4)
			assert.Equal(t, passed, calibration.Passed)
			assert.False(t, calibration.Completed.IsZero())
			assert.Equal(t, j.CalibrationKey(), calibration.Key)
			other, err := New(cfg)
			require.NoError(t, err)
			result, err = other.Judge(t.Context(), testInput())
			require.NoError(t, err)
			assert.Equal(t, passed, result.Calibrated)
			assert.Len(t, script.Requests(), 6)
		})
	}
}

func TestCalibrationInfrastructureAndTimeout(t *testing.T) {
	t.Run("provider error", func(t *testing.T) {
		failure := errors.New("offline")
		client, script := agenttest.ScriptClient(t, agenttest.Fail(failure), validTurn(), validTurn())
		cache := NewMemoryCache()
		j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: cache})
		require.NoError(t, err)
		calibration, err := j.Calibrate(t.Context())
		require.ErrorIs(t, err, failure)
		assert.False(t, calibration.Passed)
		_, ok := cache.Load(j.CalibrationKey())
		assert.False(t, ok)
		for range 2 {
			result, err := j.Judge(t.Context(), testInput())
			require.NoError(t, err)
			assert.False(t, result.Calibrated)
		}
		assert.Len(t, script.Requests(), 3)
	})
	t.Run("deadline", func(t *testing.T) {
		j, err := New(Config{Client: blockingClient{}, ModelID: "blocking", MaxOutputTokens: 512, Cache: NewMemoryCache(), CalibrationTimeout: 50 * time.Millisecond})
		require.NoError(t, err)
		_, err = j.Calibrate(t.Context())
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestCalibrationDoesNotCorrect(t *testing.T) {
	turns := calibrationTurns()
	turns[0] = agenttest.Respond(agenttest.Text("no tool"))
	client, script := agenttest.ScriptClient(t, turns...)
	cache := NewMemoryCache()
	j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: cache})
	require.NoError(t, err)
	calibration, err := j.Calibrate(t.Context())
	require.NoError(t, err)
	assert.False(t, calibration.Passed)
	assert.NotEmpty(t, calibration.Examples[0].Err)
	assert.Len(t, script.Requests(), 4)
	cached, ok := cache.Load(j.CalibrationKey())
	assert.True(t, ok)
	assert.False(t, cached.Passed)
}

func TestCalibrationKeyAndRecalibrate(t *testing.T) {
	cfg := Config{Client: unusedClient{}, ModelID: "judge-model", MaxOutputTokens: 512, Cache: NewMemoryCache()}
	baseline, err := New(cfg)
	require.NoError(t, err)
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"model", func(c *Config) {
			c.ModelID = "other"
		}},
		{"instructions", func(c *Config) {
			c.Instructions = "Be precise."
		}},
		{"output tokens", func(c *Config) {
			c.MaxOutputTokens = 1024
		}},
		{"examples", func(c *Config) {
			c.Examples = []Example{{ID: "extra", Input: testInput(), Want: map[string]Label{"a": Entailed, "b": Contradicted}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := cfg
			tt.edit(&changed)
			j, err := New(changed)
			require.NoError(t, err)
			assert.NotEqual(t, baseline.CalibrationKey(), j.CalibrationKey())
		})
	}
	client, script := agenttest.ScriptClient(t, calibrationTurns()...)
	cfg.Client = client
	cfg.Recalibrate = true
	require.NoError(t, cfg.Cache.Store(&Calibration{Key: baseline.CalibrationKey(), Passed: false, Completed: time.Now().UTC()}))
	j, err := New(cfg)
	require.NoError(t, err)
	calibration, err := j.Calibrate(t.Context())
	require.NoError(t, err)
	assert.True(t, calibration.Passed)
	assert.Len(t, script.Requests(), 4)
}

func TestCalibrationConcurrentJudgments(t *testing.T) {
	turns := calibrationTurns()
	for range 8 {
		turns = append(turns, validTurn())
	}
	client, script := agenttest.ScriptClient(t, turns...)
	j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: NewMemoryCache()})
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := j.Judge(t.Context(), testInput())
			if assert.NoError(t, err) {
				assert.True(t, result.Calibrated)
			}
		})
	}
	wg.Wait()
	assert.Len(t, script.Requests(), 12)
}

func TestCalibrationExtraExamples(t *testing.T) {
	turns := append(calibrationTurns(), validTurn())
	client, script := agenttest.ScriptClient(t, turns...)
	examples := []Example{{ID: "extra", Input: testInput(), Want: map[string]Label{"a": Entailed, "b": Contradicted}}}
	j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: NewMemoryCache(), Examples: examples})
	require.NoError(t, err)
	examples[0].Want["a"] = Contradicted
	examples[0].Input.Claims[0].Text = "mutated"
	calibration, err := j.Calibrate(t.Context())
	require.NoError(t, err)
	assert.True(t, calibration.Passed)
	assert.Len(t, calibration.Examples, 5)
	assert.Len(t, script.Requests(), 5)
}

func TestLiveGating(t *testing.T) {
	for _, env := range []string{"", "2"} {
		t.Run("env_"+env, func(t *testing.T) {
			t.Setenv("LOOM_LIVE_MODEL", env)
			client, script := agenttest.ScriptClient(t)
			_, err := New(Config{Client: client, ModelID: "live-model", MaxOutputTokens: 512, Live: true})
			if env == "" {
				require.ErrorIs(t, err, agenttest.ErrLiveModelDisabled)
			} else {
				require.ErrorContains(t, err, "must be 0 or 1")
			}
			assert.Empty(t, script.Requests())
		})
	}
	t.Setenv("LOOM_LIVE_MODEL", "")
	client, script := agenttest.ScriptClient(t, validTurn())
	j := newTestJudge(t, client)
	_, err := j.Judge(t.Context(), testInput())
	require.NoError(t, err)
	assert.Len(t, script.Requests(), 1)
}

func calibrationTurns() []agenttest.Turn {
	return []agenttest.Turn{calibrationTurn(Entailed), calibrationTurn(Contradicted), calibrationTurn(NotAddressed), calibrationTurn(Indeterminate)}
}

func calibrationTurn(label Label) agenttest.Turn {
	return agenttest.Respond(agenttest.RawToolCall(toolName, rawjson.Message(`{"arrival":{"label":"`+string(label)+`","rationale":"Evidence for the label."}}`)))
}

func (blockingClient) Complete(ctx context.Context, _ *model.Request) (*model.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
