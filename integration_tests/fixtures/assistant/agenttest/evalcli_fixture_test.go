package agenttest_test

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"testing"

	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalCLIStory(t *testing.T) {
	cfg := fixtureEvalCLI()
	cfg.StoreDir = t.TempDir()
	var out, diagnostic bytes.Buffer
	cfg.Stdout, cfg.Stderr = &out, &diagnostic
	goodFactory := cfg.Harness
	built := 0
	cfg.Harness = func(ctx context.Context, v eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		built++
		if built == 2 || built == 4 {
			return eval.NewHarnessFunc(guideRegister, nil, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready"))))(ctx, v, opts...)
		}
		return goodFactory(ctx, v, opts...)
	}
	// Story (b), command 1: a baseline with two quality failures still exits zero.
	code := evalcli.Main(t.Context(), []string{"run", "--scenario", "status", "--repeat", "5", "--label", "baseline"}, cfg)
	require.Equal(t, 0, code, diagnostic.String())
	assert.Contains(t, out.String(), "3/5 passed")
	store, err := eval.Open(cfg.StoreDir)
	require.NoError(t, err)
	baseline, err := store.Resolve("last")
	require.NoError(t, err)
	out.Reset()
	diagnostic.Reset()
	cfg.Harness = goodFactory
	// Command 2: the revised model script passes every repeat and compares to baseline.
	code = evalcli.Main(t.Context(), []string{"run", "--scenario", "status", "--repeat", "5", "--label", "clearer-policy", "--compare", "last"}, cfg)
	require.Equal(t, 0, code, diagnostic.String())
	assert.Contains(t, out.String(), "5/5 passed")
	assert.Contains(t, out.String(), "Compare "+baseline)
	assert.Contains(t, out.String(), "Improved:")
	out.Reset()
	diagnostic.Reset()
	// Command 3: inspect the second, failing baseline attempt from its stored export.
	code = evalcli.Main(t.Context(), []string{"show", baseline + "/#1"}, cfg)
	require.Equal(t, 0, code, diagnostic.String())
	assert.Contains(t, out.String(), "verdict failed")
	assert.Contains(t, out.String(), "expected_tools")
	assert.Contains(t, out.String(), "ready")
	e, err := store.Load(baseline)
	require.NoError(t, err)
	require.Len(t, e.Attempts[1].Runs, 1)
	_, err = e.Attempts[1].Runs[0].Trajectory()
	require.NoError(t, err)
}

func TestEvalCLITokenLimitFixture(t *testing.T) {
	cfg := fixtureEvalCLI()
	cfg.StoreDir = t.TempDir()
	cfg.Harness = runnerFactory(agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("ready"), agenttest.Usage(60, 40))))
	cfg.Scenarios = []eval.Scenario{runnerScenario()}
	var out, diagnostic bytes.Buffer
	cfg.Stdout, cfg.Stderr = &out, &diagnostic
	code := evalcli.Main(t.Context(), []string{"run", "--repeat", "5", "--max-tokens", "250"}, cfg)
	require.Equal(t, 0, code, diagnostic.String())
	store, err := eval.Open(cfg.StoreDir)
	require.NoError(t, err)
	headers, err := store.List()
	require.NoError(t, err)
	require.Len(t, headers, 1)
	assert.Equal(t, "stopped", headers[0].Status)
	assert.Equal(t, eval.StopTokenLimit, headers[0].Stop)
	assert.Equal(t, 300, headers[0].Tokens)
}

func Example_evalCLI() {
	register := guideRegister
	scenarios := []eval.Scenario{runnerScenario()}
	// snippet:start eval-cli-main
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := evalcli.Main(ctx, os.Args[1:], evalcli.Config{
		Harness:   eval.NewHarnessFunc(register, nil),
		Scenarios: scenarios,
	})
	stop()
	os.Exit(code)
	// snippet:end eval-cli-main
}

// snippet:start eval-cli-fixture
func fixtureEvalCLI() evalcli.Config {
	return evalcli.Config{
		Harness: eval.NewHarnessFunc(guideRegister, func(h *agenttest.Harness, _ eval.Variant) error {
			agenttest.Stub(h, projected.ProjectedStatusToolTool).
				Return(&projected.ProjectedStatusToolResult{Status: "ready"})
			return nil
		}, agenttest.ScriptedModel("fixture",
			agenttest.Respond(agenttest.ToolCall(projected.ProjectedStatusToolTool, &projected.ProjectedStatusToolPayload{})),
			agenttest.Respond(agenttest.Text("ready")),
		)),
		Scenarios: []eval.Scenario{{
			ID: "status", Agent: "assistant.assistant_runtime",
			Items: eval.Items(eval.Item{ID: "one", Input: "status?", ExpectedTools: &expect.ExpectedTools{
				Exact: true, Tools: []expect.ExpectedTool{{Name: projected.ProjectedStatusTool}},
			}}),
			Expect: []expect.Expectation{expect.Completed()},
		}},
	}
}

// snippet:end eval-cli-fixture
