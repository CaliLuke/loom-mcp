package eval_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func planConfig() eval.Config {
	s := customScenario(syntheticSession())
	s.ID = "one"
	s.Tags = []string{"red"}
	s.Items = eval.Items(eval.Item{ID: "a", Input: "a"}, eval.Item{ID: "b", Input: "b"})
	second := s
	second.ID = "two"
	second.Tags = []string{"blue"}
	return eval.Config{Harness: func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
		return nil, nil
	}, Scenarios: []eval.Scenario{s, second}, Variants: []eval.Variant{{ID: "v1"}, {ID: "v2"}}}
}

func TestPlanMatrix(t *testing.T) {
	cfg := planConfig()
	cfg.Repeats = 3
	keys, err := eval.Plan(cfg)
	require.NoError(t, err)
	require.Len(t, keys, 24)
	assert.Equal(t, eval.AttemptKey{Variant: "v1", Scenario: "one", Item: "a", Repeat: 1}, keys[0])
	assert.Equal(t, "v2:two:b:3", keys[23].String())
	cfg.Variants = nil
	cfg.Repeats = 0
	keys, err = eval.Plan(cfg)
	require.NoError(t, err)
	require.Len(t, keys, 4)
	assert.Equal(t, eval.DefaultVariant, keys[0].Variant)
}

func TestSelection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection eval.Selection
		count     int
		messages  []string
	}{
		{name: "all", count: 8},
		{name: "variants", selection: eval.Selection{Variants: []string{"v1"}}, count: 4},
		{name: "scenarios", selection: eval.Selection{Scenarios: []string{"two"}}, count: 4},
		{name: "tags", selection: eval.Selection{Tags: []string{"red"}}, count: 4},
		{name: "bare item", selection: eval.Selection{Items: []string{"a"}}, count: 4},
		{name: "qualified", selection: eval.Selection{Items: []string{"one/a"}}, count: 2},
		{name: "OR", selection: eval.Selection{Items: []string{"a", "b"}}, count: 8},
		{name: "AND", selection: eval.Selection{Variants: []string{"v2"}, Tags: []string{"red"}, Items: []string{"a"}}, count: 1},
		{name: "joined", selection: eval.Selection{Variants: []string{"", "v1", "v1", "missing"}}, messages: []string{"empty", "duplicate", "unknown"}},
		{name: "partial unmatched", selection: eval.Selection{Scenarios: []string{"one", "two"}, Tags: []string{"red"}}, messages: []string{"matches nothing"}},
		{name: "empty plan", selection: eval.Selection{Scenarios: []string{"two"}, Items: []string{"one/a"}}, messages: []string{"matches nothing", "empty plan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := planConfig()
			cfg.Select = tc.selection
			keys, err := eval.Plan(cfg)
			if len(tc.messages) > 0 {
				require.Error(t, err)
				for _, m := range tc.messages {
					assert.Contains(t, err.Error(), m)
				}
				return
			}
			require.NoError(t, err)
			assert.Len(t, keys, tc.count)
		})
	}
}

func TestConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*eval.Config)
		message string
	}{
		{"nil harness", func(c *eval.Config) {
			c.Harness = nil
		}, "Harness"},
		{"no scenarios", func(c *eval.Config) {
			c.Scenarios = nil
		}, "scenarios"},
		{"all scenarios validated", func(c *eval.Config) {
			c.Scenarios[1].Agent = ""
			c.Scenarios[1].Drive = nil
			c.Select.Scenarios = []string{"one"}
		}, "Agent"},
		{"invalid variant", func(c *eval.Config) {
			c.Variants[0].ID = "bad:id"
		}, "variant"},
		{"duplicate variant", func(c *eval.Config) {
			c.Variants[1].ID = "v1"
		}, "duplicate"},
		{"labels", func(c *eval.Config) {
			c.Variants[0].Labels = map[string]string{"": "x", "bad": "\xff"}
		}, "label"},
		{"policy", func(c *eval.Config) {
			c.Variants[0].Policy = &api.PolicyOverrides{MaxToolCalls: -1}
		}, "Policy"},
		{"repeats", func(c *eval.Config) {
			c.Repeats = -1
		}, "Repeats"},
		{"concurrency", func(c *eval.Config) {
			c.Concurrency = -1
		}, "Concurrency"},
		{"tokens", func(c *eval.Config) {
			c.MaxTokens = -1
		}, "MaxTokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := planConfig()
			tc.change(&cfg)
			keys, err := eval.Plan(cfg)
			assert.Nil(t, keys)
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestSelectionRejectsEveryKind(t *testing.T) {
	for _, tc := range []struct {
		name, known string
		set         func(*eval.Selection, []string)
	}{
		{"variant", "v1", func(s *eval.Selection, values []string) {
			s.Variants = values
		}},
		{"scenario", "one", func(s *eval.Selection, values []string) {
			s.Scenarios = values
		}},
		{"tag", "red", func(s *eval.Selection, values []string) {
			s.Tags = values
		}},
		{"item", "one/a", func(s *eval.Selection, values []string) {
			s.Items = values
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := planConfig()
			tc.set(&cfg.Select, []string{"", tc.known, tc.known, "missing"})
			_, err := eval.Plan(cfg)
			require.Error(t, err)
			for _, problem := range []string{"empty " + tc.name, "duplicate " + tc.name, "unknown " + tc.name} {
				assert.Contains(t, err.Error(), problem)
			}
		})
	}
}
