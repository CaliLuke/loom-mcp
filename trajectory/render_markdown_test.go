package trajectory_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderMarkdownGoldens(t *testing.T) {
	for name, runs := range renderFixtures(t) {
		t.Run(name, func(t *testing.T) {
			opts := trajectory.RenderOptions{Format: trajectory.FormatMarkdown, Limits: trajectory.DefaultLimits()}
			got := runs.Render(opts)
			assert.Contains(t, got, "- Run: "+runs[0].RunID)
			assert.Contains(t, got, "```json\n")
			testutil.AssertString(t, filepath.Join("testdata", "golden", name+".md.golden"), got)
			assert.Equal(t, got, runs.Render(opts))
			assertNoTrailingWhitespace(t, got)
		})
	}
}

func TestRenderMarkdownFences(t *testing.T) {
	for _, content := range []string{"```", "before ````` after", "` one `` two ```"} {
		t.Run(content, func(t *testing.T) {
			run := resultTrajectory(`{"text":"` + content + `"}`)
			got := run.Render(trajectory.RenderOptions{Format: trajectory.FormatMarkdown})
			// Every JSON opener must have a matching closer, and the result's
			// opener must exceed every run of backticks in the result.
			var fence string
			for _, line := range strings.Split(got, "\n") {
				trimmed := strings.TrimSpace(line)
				switch {
				case strings.HasSuffix(trimmed, "json") && strings.HasPrefix(trimmed, "```"):
					assert.Empty(t, fence)
					fence = strings.TrimSuffix(trimmed, "json")
				case fence != "" && trimmed == fence:
					fence = ""
				case strings.Contains(trimmed, content):
					assert.Greater(t, len(fence), longestBackticks(content))
				}
			}
			assert.Empty(t, fence)
			assert.Contains(t, got, content)
			assertNoTrailingWhitespace(t, got)
		})
	}
	got := childChain(1).Render(trajectory.RenderOptions{Format: trajectory.FormatMarkdown})
	assert.Contains(t, got, "    - Run: child-1")
}

func TestRenderMarkdownPlainTextFences(t *testing.T) {
	for _, text := range []string{"a\n```\nb", "a\nb", "a ``` b"} {
		for _, surface := range []string{"final", "error", "thinking", "clarification", "confirmation", "questions"} {
			t.Run(surface+"/"+text, func(t *testing.T) {
				run, indent := plainTextTrajectory(t, surface, text)
				got := run.Render(trajectory.RenderOptions{Format: trajectory.FormatMarkdown})
				fence := strings.Repeat("`", max(3, longestBackticks(text)+1))
				var block strings.Builder
				block.WriteString(indent + fence + "\n")
				for _, line := range strings.Split(text, "\n") {
					block.WriteString(indent + line + "\n")
				}
				block.WriteString(indent + fence + "\n")
				assert.Contains(t, got, block.String())
				assert.Contains(t, got, "- Totals:\n")
				assertNoTrailingWhitespace(t, got)
			})
		}
	}
}

func plainTextTrajectory(t *testing.T, surface, text string) (*trajectory.Trajectory, string) {
	t.Helper()
	run := &trajectory.Trajectory{StatusVerified: true}
	indent := "      "
	switch surface {
	case "final":
		run.Final = &model.Message{Parts: []model.Part{model.TextPart{Text: text}}}
		return run, "    "
	case "error":
		run.Steps = []trajectory.Step{{Kind: trajectory.StepModel, Verified: true, Model: &trajectory.ModelStep{Error: &callrecord.CallError{Message: text}}}}
		return run, indent
	}
	kind, eventType, key := trajectory.StepAwait, hooks.AwaitClarification, "Question"
	var payload any
	switch surface {
	case "thinking":
		kind, eventType, key = trajectory.StepEvent, hooks.ThinkingBlock, "Text"
	case "confirmation":
		eventType, key = hooks.AwaitConfirmation, "Prompt"
	case "questions":
		eventType = hooks.AwaitQuestions
		payload = map[string]any{"Questions": []map[string]string{{"ID": "question", "Prompt": text}}}
	}
	if payload == nil {
		payload = map[string]string{key: text}
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	run.Steps = []trajectory.Step{{Kind: kind, Verified: true, Event: &trajectory.ObservedEvent{Type: eventType, Payload: rawjson.Message(data)}}}
	return run, indent
}

func longestBackticks(value string) int {
	longest, current := 0, 0
	for _, char := range value {
		if char == '`' {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return longest
}
