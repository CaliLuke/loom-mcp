package runtime

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// agentMessageText concatenates text parts from a model.Message.
func agentMessageText(msg *model.Message) string {
	if msg == nil || len(msg.Parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range msg.Parts {
		if _, isThinking := p.(model.ThinkingPart); isThinking {
			continue
		}
		if tp, ok := p.(model.TextPart); ok && tp.Text != "" {
			b.WriteString(tp.Text)
		}
	}
	return b.String()
}

// transcriptText concatenates assistant-visible text across a transcript.
func transcriptText(msgs []*model.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if text := agentMessageText(msg); text != "" {
			b.WriteString(text)
		}
	}
	return b.String()
}

// newTextAgentMessage builds a model.Message with a single TextPart.
func newTextAgentMessage(role model.ConversationRole, text string) *model.Message {
	if text == "" {
		return nil
	}
	return &model.Message{
		Role:  role,
		Parts: []model.Part{model.TextPart{Text: text}},
	}
}

// defaultToolTitle derives a human-friendly title from a fully-qualified tool id.
func defaultToolTitle(id tools.Ident) string {
	s := string(id)
	if last := lastSegment(s, '.'); last != "" {
		s = last
	}
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.Join(strings.Fields(s), " ")
	var b strings.Builder
	for i, w := range strings.Fields(s) {
		if i > 0 {
			b.WriteByte(' ')
		}
		if len(w) == 0 {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		for j := 1; j < len(r); j++ {
			r[j] = unicode.ToLower(r[j])
		}
		b.WriteString(string(r))
	}
	return b.String()
}

// lastSegment returns the last segment of a string after the last separator.
func lastSegment(s string, sep rune) string {
	for i := len(s) - 1; i >= 0; i-- {
		if rune(s[i]) == sep {
			if i+1 < len(s) {
				return s[i+1:]
			}
			return ""
		}
	}
	return s
}

// ConvertRunOutputToToolResult converts a nested agent RunOutput into a planner.ToolResult.
func ConvertRunOutputToToolResult(toolName tools.Ident, output *RunOutput) planner.ToolResult {
	result := planner.ToolResult{
		Name:   toolName,
		Result: finalRunOutputText(output),
	}
	result.ChildrenCount = len(output.ToolEvents)
	applyChildRunSummary(toolName, output, &result)
	return result
}

func finalRunOutputText(output *RunOutput) string {
	if output.Final == nil {
		return ""
	}
	return agentMessageText(output.Final)
}

func applyChildRunSummary(toolName tools.Ident, output *RunOutput, result *planner.ToolResult) {
	if len(output.ToolEvents) == 0 {
		return
	}
	summary := summarizeChildToolEvents(output.ToolEvents)
	if summary.allFailed() {
		result.Error = summary.asToolError(toolName)
	}
	if telemetry := summary.asTelemetry(); telemetry != nil {
		result.Telemetry = telemetry
	}
}

type childToolSummary struct {
	eventCount      int
	totalTokens     int
	totalDurationMs int64
	models          []string
	failedCount     int
	lastError       error
}

func summarizeChildToolEvents(events []*api.ToolEvent) childToolSummary {
	summary := childToolSummary{eventCount: len(events)}
	modelSeen := make(map[string]bool)
	for _, event := range events {
		if event.Telemetry != nil {
			summary.totalTokens += event.Telemetry.TokensUsed
			summary.totalDurationMs += event.Telemetry.DurationMs
			if event.Telemetry.Model != "" && !modelSeen[event.Telemetry.Model] {
				summary.models = append(summary.models, event.Telemetry.Model)
				modelSeen[event.Telemetry.Model] = true
			}
		}
		if event.Error != nil {
			summary.failedCount++
			summary.lastError = event.Error
		}
	}
	return summary
}

func (s childToolSummary) allFailed() bool {
	return s.failedCount > 0 && s.failedCount == s.eventCount
}

func (s childToolSummary) asToolError(toolName tools.Ident) *planner.ToolError {
	if s.failedCount == 1 {
		return planner.NewToolErrorWithCause(fmt.Sprintf("agent-tool %q: nested tool failed", toolName), s.lastError)
	}
	return planner.NewToolErrorWithCause(fmt.Sprintf("agent-tool %q: all %d nested tools failed", toolName, s.failedCount), s.lastError)
}

func (s childToolSummary) asTelemetry() *telemetry.ToolTelemetry {
	if s.totalTokens == 0 && s.totalDurationMs == 0 && len(s.models) == 0 {
		return nil
	}
	t := &telemetry.ToolTelemetry{
		TokensUsed: s.totalTokens,
		DurationMs: s.totalDurationMs,
	}
	if len(s.models) > 0 {
		t.Model = s.models[0]
	}
	return t
}
