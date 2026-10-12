package judging

import (
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// render contains renderer panics within one batch and compares actual output,
// rather than markers, to detect elision.
func render(out *eval.Outcome, b batch, limits trajectory.Limits) (text string, truncated bool, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("judging: render %s: %v", b.subject, value)
		}
	}()
	session := out.Session.Trajectory
	if len(session) == 0 {
		return "", false, nil
	}
	last := session[len(session)-1]
	var renderWith func(trajectory.RenderOptions) string
	switch b.subject {
	case eval.SubjectAnswer:
		if last.Final == nil {
			return "", false, nil
		}
		parts := make([]string, len(last.Final.Parts))
		for i, p := range last.Final.Parts {
			parts[i] = answerPart(p)
		}
		return strings.Join(parts, "\n"), false, nil
	case eval.SubjectTrajectory:
		renderWith = last.Render
	case eval.SubjectSession:
		renderWith = session.Render
	case eval.SubjectStep:
		prefix := stepPrefix(session, *b.step)
		renderWith = func(opts trajectory.RenderOptions) string {
			locator := fmt.Sprintf("Judged call: run %d step %d", b.step.Run+1, b.step.Index)
			if b.step.Tool != "" {
				locator += fmt.Sprintf(" %s %s", b.step.Tool, b.step.CallID)
			}
			return prefix.Render(opts) + locator + "\n"
		}
	default:
		return "", false, fmt.Errorf("judging: unknown subject %q", b.subject)
	}
	text = renderWith(trajectory.RenderOptions{Limits: limits})
	return text, text != renderWith(trajectory.RenderOptions{}), nil
}

// answerPart matches trajectory's final-message placeholders without rendering
// a run header or applying value limits to the answer subject.
func answerPart(part model.Part) string {
	switch p := part.(type) {
	case model.TextPart:
		return p.Text
	case *model.TextPart:
		return p.Text
	case model.ThinkingPart, *model.ThinkingPart:
		return "[thinking part]"
	case model.ImagePart, *model.ImagePart:
		return "[image part]"
	case model.DocumentPart, *model.DocumentPart:
		return "[document part]"
	case model.CitationsPart, *model.CitationsPart:
		return "[citations part]"
	case model.ToolUsePart, *model.ToolUsePart:
		return "[tool_use part]"
	case model.ToolResultPart, *model.ToolResultPart:
		return "[tool_result part]"
	case model.CacheCheckpointPart, *model.CacheCheckpointPart:
		return "[cache_checkpoint part]"
	default:
		panic("unsupported final message part")
	}
}
