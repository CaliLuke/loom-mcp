package agenttest_test

import (
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/judge"
	"github.com/CaliLuke/loom-mcp/v2/eval/judging"
	"github.com/CaliLuke/loom-mcp/v2/features/model/anthropic"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgingStoreFixture(t *testing.T) {
	client, script := agenttest.ScriptClient(t,
		fixtureJudgment("arrival", "entailed"), fixtureJudgment("arrival", "contradicted"), fixtureJudgment("arrival", "not_addressed"), fixtureJudgment("arrival", "indeterminate"), fixtureJudgment("task_completion", "entailed"))
	base, err := judge.New(judge.Config{Client: client, ModelID: "scripted-judge", MaxOutputTokens: 1024, Cache: judge.NewMemoryCache()})
	require.NoError(t, err)
	runJudgingFixture(t, base)
	assert.Len(t, script.Requests(), 5)
}

func TestLiveJudgingStoreFixture(t *testing.T) {
	const modelID = "claude-sonnet-4-5-20250929"
	agenttest.SkipUnlessLive(t, modelID)
	if testing.Short() {
		t.Skip("live judging is outside the short-test gate")
	}
	provider, err := anthropic.NewFromAPIKey(os.Getenv("ANTHROPIC_API_KEY"), modelID)
	require.NoError(t, err)
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	base, err := judge.New(judge.Config{Client: client, ModelID: modelID, MaxOutputTokens: 1024, Cache: judge.NewMemoryCache(), Live: true})
	require.NoError(t, err)
	runJudgingFixture(t, base)
}

func runJudgingFixture(t *testing.T, base *judge.Judge) {
	t.Helper()
	adapter, err := judging.New(judging.Config{Judge: base})
	require.NoError(t, err)
	// snippet:start eval-judging
	scenario := eval.Scenario{
		ID: "greeting", Agent: "assistant.assistant_runtime", Task: "Say hello",
		Items:  eval.Items(eval.Item{ID: "hello", Input: "Say hello"}),
		Claims: []eval.Claim{eval.TaskCompletion()},
	}
	cfg := eval.Config{
		Harness: eval.NewHarnessFunc(registerScenarioFixture, nil,
			agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.Text("Hello!")))),
		Scenarios: []eval.Scenario{scenario}, Judge: adapter,
	}
	// snippet:end eval-judging
	store, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	cfg.OnAttempt = writer.OnAttempt(nil)
	report, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.NotNil(t, report.Judge)
	writer.SetJudge(*report.Judge)
	require.NoError(t, writer.Finish(report, nil))
	experiment, err := store.Load(writer.ID())
	require.NoError(t, err)
	require.Len(t, experiment.Attempts, 1)
	stored := experiment.Attempts[0]
	assert.Equal(t, eval.VerdictPassed, stored.Verdict)
	require.Len(t, stored.Judgments, 1)
	assert.Equal(t, "task_completion", stored.Judgments[0].ClaimID)
	assert.Equal(t, "trajectory", stored.Judgments[0].Subject)
	assert.Equal(t, "entailed", stored.Judgments[0].Label)
	assert.NotEmpty(t, stored.Judgments[0].Rationale)
	assert.Equal(t, "The agent completed the task: Say hello.", stored.Claims[0].Text)
	assert.Equal(t, report.Judge, experiment.Header.Judge)
	assert.Positive(t, stored.JudgeTokens)
	assert.Equal(t, stored.Tokens+stored.JudgeTokens, experiment.Header.Tokens)
	assert.Len(t, stored.Runs, 1)
}

func fixtureJudgment(id, label string) agenttest.Turn {
	return agenttest.Respond(agenttest.RawToolCall("record_judgments", rawjson.Message(`{"`+id+`":{"label":"`+label+`","rationale":"The visible evidence supports this label."}}`)), agenttest.Usage(2, 1))
}
