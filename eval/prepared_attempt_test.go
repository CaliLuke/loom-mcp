package eval

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedAdmission(t *testing.T) {
	cfg := storeConfig()
	cfg.Harness = nil
	cfg.PrepareAttempt = func(context.Context, PreparedAttemptInfo) (*PreparedAttempt, error) {
		return nil, assert.AnError
	}
	cfg.PrepareTimeout = time.Second
	cfg.CleanupTimeout = time.Second
	_, err := Plan(cfg)
	require.NoError(t, err, "detached prepared plan requires no writer")
	for _, test := range []struct {
		name   string
		change func(*Config)
	}{
		{"prepare timeout", func(c *Config) {
			c.PrepareTimeout = 0
		}},
		{"cleanup timeout", func(c *Config) {
			c.CleanupTimeout = -time.Second
		}},
		{"legacy factory", func(c *Config) {
			c.Harness = storeConfig().Harness
		}},
		{"scenario driver", func(c *Config) {
			c.Scenarios[0].BuildInputs = func(context.Context, Item) ([]agenttest.Input, error) {
				return nil, nil
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			copyCfg := cfg
			copyCfg.Scenarios = append([]Scenario(nil), cfg.Scenarios...)
			test.change(&copyCfg)
			_, err := Plan(copyCfg)
			assert.Error(t, err)
		})
	}
	_, err = Run(t.Context(), cfg)
	assert.ErrorContains(t, err, "writer")
}
