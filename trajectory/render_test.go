package trajectory_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderTextGoldens(t *testing.T) {
	for name, runs := range renderFixtures(t) {
		t.Run(name, func(t *testing.T) {
			opts := trajectory.RenderOptions{Format: trajectory.FormatText, Limits: trajectory.DefaultLimits()}
			got := runs.Render(opts)
			assert.Contains(t, got, "Run: "+runs[0].RunID)
			testutil.AssertString(t, filepath.Join("testdata", "golden", name+".txt.golden"), got)
			assert.Equal(t, got, runs.Render(opts))
			if len(runs) == 1 {
				assert.Equal(t, got, runs[0].Render(opts))
			}
			assertNoTrailingWhitespace(t, got)
		})
	}
}

func TestRenderLimits(t *testing.T) {
	assert.Equal(t, trajectory.Limits{MaxValueBytes: 4096, MaxSteps: 200, MaxDepth: 4}, trajectory.DefaultLimits())
	for _, test := range []struct {
		name   string
		run    *trajectory.Trajectory
		limits trajectory.Limits
		marker string
		absent string
	}{
		{"large result", resultTrajectory(`"` + strings.Repeat("x", 5*1024*1024-2) + `"`), trajectory.Limits{MaxValueBytes: 2048}, "…[truncated: 2048 of 5242880 bytes]", strings.Repeat("x", 2049)},
		{"UTF-8", resultTrajectory("ééé"), trajectory.Limits{MaxValueBytes: 4}, "…[truncated: 3 of 8 bytes]", "éé"},
		{"steps", repeatedSteps(40), trajectory.Limits{MaxSteps: 3}, "[37 more steps omitted]", "Step 4"},
		{"depth", childChain(5), trajectory.Limits{MaxDepth: 4}, "[child run child-5 omitted: depth limit 4]", "Run: child-5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, format := range []trajectory.Format{trajectory.FormatText, trajectory.FormatMarkdown} {
				got := test.run.Render(trajectory.RenderOptions{Format: format, Limits: test.limits})
				assert.Contains(t, got, test.marker)
				assert.NotContains(t, got, test.absent)
				assert.True(t, utf8.ValidString(got))
				assertNoTrailingWhitespace(t, got)
			}
		})
	}
	// Zero limits retain every step and child, and never impose a total budget.
	assert.NotContains(t, repeatedSteps(201).Render(trajectory.RenderOptions{}), "omitted")
	assert.Contains(t, childChain(5).Render(trajectory.RenderOptions{}), "Run: child-5")
	assert.NotContains(t, resultTrajectory(strings.Repeat("x", 5000)).Render(trajectory.RenderOptions{}), "truncated")
}

func TestRenderValuesAndTrust(t *testing.T) {
	run := resultTrajectory(` { "z":9007199254740993, "a":1.2300, "e":1e+09 } `)
	run.Status = "success"
	run.Steps[0].Verified = true
	run.Steps = append(run.Steps, trajectory.Step{Kind: trajectory.StepEvent, Step: 1, Event: &trajectory.ObservedEvent{
		Type: hooks.ThinkingBlock, Timestamp: time.Date(2026, 10, 11, 12, 0, 0, 123, time.FixedZone("offset", 3600)),
		Payload: rawjson.Message(`{"Text":"thinking\nnext line"}`),
	}})
	got := run.Render(trajectory.RenderOptions{})
	assert.Contains(t, got, `{"a":1.2300,"e":1e+09,"z":9007199254740993}`)
	assert.Contains(t, got, "Status: success (unverified)")
	assert.Equal(t, 1, strings.Count(got, "(observed, unverified)"))
	assert.Contains(t, got, "2026-10-11T11:00:00.000000123Z")
	assert.Contains(t, got, "thinking\n")
	run.StatusVerified = true
	run.Steps[1].Verified = true
	assert.NotContains(t, run.Render(trajectory.RenderOptions{}), "unverified")
	assert.Contains(t, resultTrajectory("plain\ntext").Render(trajectory.RenderOptions{}), `"plain\ntext"`)
	assert.Equal(t, got, runWithTrustReset(run).Render(trajectory.RenderOptions{}))
}

func TestRenderFinalParts(t *testing.T) {
	run := &trajectory.Trajectory{Final: &model.Message{Parts: []model.Part{
		model.TextPart{Text: "first  \nsecond\t"}, &model.TextPart{Text: "pointer text"},
		model.ThinkingPart{}, &model.ImagePart{}, model.DocumentPart{}, model.CitationsPart{},
		model.ToolUsePart{}, model.ToolResultPart{}, model.CacheCheckpointPart{},
	}}}
	got := run.Render(trajectory.RenderOptions{})
	for _, text := range []string{"first\n", "second\n", "pointer text", "[thinking part]", "[image part]", "[document part]", "[citations part]", "[tool_use part]", "[tool_result part]", "[cache_checkpoint part]"} {
		assert.Contains(t, got, text)
	}
	assertNoTrailingWhitespace(t, got)
}

func TestRenderPreservesEvidenceAndShortensHashes(t *testing.T) {
	run, err := trajectory.Build(treeRecord(t), "run", trajectory.Options{})
	require.NoError(t, err)
	before, err := run.Marshal()
	require.NoError(t, err)
	for _, format := range []trajectory.Format{trajectory.FormatText, trajectory.FormatMarkdown} {
		got := run.Render(trajectory.RenderOptions{Format: format, Limits: trajectory.DefaultLimits()})
		assert.NotContains(t, got, string(run.Steps[0].Model.RequestDigest))
		digest := strings.TrimPrefix(string(run.Steps[0].Model.RequestDigest), "sha256:")
		assert.Contains(t, got, "Request digest: sha256:"+digest[:12]+"\n")
	}
	after, err := run.Marshal()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestRenderModelVerificationMarker(t *testing.T) {
	for _, format := range []trajectory.Format{trajectory.FormatText, trajectory.FormatMarkdown} {
		for _, verified := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/verified=%t", format, verified), func(t *testing.T) {
				run := &trajectory.Trajectory{StatusVerified: true, Steps: []trajectory.Step{{
					Kind: trajectory.StepModel, Step: 1, Verified: verified, Model: &trajectory.ModelStep{},
				}}}
				got := run.Render(trajectory.RenderOptions{Format: format})
				if verified {
					assert.NotContains(t, got, "(observed, unverified)")
				} else {
					assert.Contains(t, got, "Model: 0/0  (observed, unverified)")
				}
			})
		}
	}
}

func TestRenderAwaitQuestionsTitle(t *testing.T) {
	for _, title := range []string{"null", `""`, `"Preferences"`} {
		for _, format := range []trajectory.Format{trajectory.FormatText, trajectory.FormatMarkdown} {
			t.Run(fmt.Sprintf("%s/%s", title, format), func(t *testing.T) {
				run := &trajectory.Trajectory{Steps: []trajectory.Step{{Kind: trajectory.StepAwait, Event: &trajectory.ObservedEvent{
					Type: hooks.AwaitQuestions, Payload: rawjson.Message(`{"ID":"questions","Title":` + title + `,"Questions":[]}`),
				}}}}
				got := run.Render(trajectory.RenderOptions{Format: format})
				if title == `"Preferences"` {
					assert.Contains(t, got, "Title: Preferences\n")
				} else {
					assert.NotContains(t, got, "Title:")
				}
			})
		}
	}
}

func renderFixtures(t *testing.T) map[string]trajectory.Session {
	t.Helper()
	tree, err := trajectory.Build(treeRecord(t), "run", trajectory.Options{})
	require.NoError(t, err)
	await := &trajectory.Trajectory{RunID: "await-run", SessionID: "session", AgentID: "agent", Status: "success"}
	for i, event := range []struct {
		kind    hooks.EventType
		payload string
	}{
		{hooks.AwaitClarification, `{"ID":"clarify","Question":"Which café?\nChoose one.","MissingFields":["city"]}`},
		{hooks.AwaitQuestions, `{"ID":"questions","Title":"Preferences","Questions":[{"ID":"color","Prompt":"Which color?","Options":[{"ID":"blue","Label":"Blue"}]}]}`},
		{hooks.AwaitTypedInput, `{"ID":"typed","Title":"Enter details","Schema":{"type":"object"}}`},
		{hooks.AwaitConfirmation, `{"ID":"confirm","Title":"Confirm action","Prompt":"Proceed?\nThis writes data.","ToolCallID":"write"}`},
		{hooks.AwaitExternalTools, `{"ID":"external","Items":[{"ToolName":"external.fetch","ToolCallID":"fetch","Payload":{"id":9007199254740993}}]}`},
	} {
		await.Steps = append(await.Steps, trajectory.Step{Kind: trajectory.StepAwait, Step: i + 1, Event: &trajectory.ObservedEvent{
			Type: event.kind, EventKey: string(event.kind), Timestamp: time.Unix(int64(i+1), 0), Payload: rawjson.Message(event.payload),
		}})
	}
	await.Steps = append(await.Steps,
		trajectory.Step{Kind: trajectory.StepAnswer, Step: 6, Verified: true, Answer: &trajectory.AnswerStep{Kind: index.Clarification, Content: []byte(`"The café in Paris"`)}},
		trajectory.Step{Kind: trajectory.StepAnswer, Step: 6, Verified: true, Answer: &trajectory.AnswerStep{Kind: index.TypedInput, InputID: "typed", Content: []byte(`{"z":9007199254740993,"a":1.2300}`)}},
		trajectory.Step{Kind: trajectory.StepAnswer, Step: 6, Event: &trajectory.ObservedEvent{Type: hooks.ToolAuthorization, Timestamp: time.Unix(7, 0), Payload: rawjson.Message(`{"ID":"confirm","Approved":true}`)}},
	)
	return map[string]trajectory.Session{"tree": {tree}, "await": {await}, "session": {tree, await}}
}

func resultTrajectory(result string) *trajectory.Trajectory {
	return &trajectory.Trajectory{RunID: "run", Steps: []trajectory.Step{{Kind: trajectory.StepTool, Step: 1, Verified: true, Tool: &trajectory.ToolStep{Name: "test.tool", ToolCallID: "call", Result: rawjson.Message(result)}}}}
}

func repeatedSteps(count int) *trajectory.Trajectory {
	run := &trajectory.Trajectory{}
	for i := range count {
		run.Steps = append(run.Steps, trajectory.Step{Kind: trajectory.StepTool, Step: i + 1, Verified: true, Tool: &trajectory.ToolStep{}})
	}
	return run
}

func childChain(count int) *trajectory.Trajectory {
	root := resultTrajectory("null")
	current := root
	for i := range count {
		child := resultTrajectory("null")
		child.RunID = fmt.Sprintf("child-%d", i+1)
		current.Steps[0].Tool.Children = []*trajectory.Trajectory{child}
		current = child
	}
	return root
}

func runWithTrustReset(run *trajectory.Trajectory) *trajectory.Trajectory {
	run.StatusVerified = false
	run.Steps[1].Verified = false
	return run
}

func assertNoTrailingWhitespace(t *testing.T, got string) {
	t.Helper()
	for _, line := range strings.Split(got, "\n") {
		assert.Equal(t, strings.TrimRight(line, " \t\r"), line)
	}
}
