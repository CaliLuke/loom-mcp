package eval

import (
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// metrics sums run-local evidence, including descendants exactly once.
func metrics(session trajectory.Session) []expect.Score {
	scores := []expect.Score{{Name: "model_calls"}, {Name: "tool_calls"}, {Name: "failed_tool_calls"}, {Name: "repeated_tool_calls"}}
	var visit func(*trajectory.Trajectory)
	visit = func(t *trajectory.Trajectory) {
		if t == nil {
			return
		}
		scores[0].Value += float64(t.Totals.AdoptedModelCalls)
		scores[1].Value += float64(t.Totals.ToolCalls)
		scores[2].Value += float64(t.Totals.FailedToolCalls)
		scores[3].Value += float64(t.Totals.RepeatedToolCalls)
		for _, s := range t.Steps {
			if s.Tool != nil {
				for _, child := range s.Tool.Children {
					visit(child)
				}
			}
		}
	}
	for _, t := range session {
		visit(t)
	}
	return scores
}
