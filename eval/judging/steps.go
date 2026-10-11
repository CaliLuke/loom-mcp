package judging

import (
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// steps selects only root-run units in session and trajectory-index order.
// A model unit can choose several tools, so its tool/call locator stays empty.
func steps(session trajectory.Session, tool tools.Ident) []eval.JudgedStep {
	var units []eval.JudgedStep
	for k, run := range session {
		if run == nil {
			continue
		}
		for i, s := range run.Steps {
			unit := eval.JudgedStep{Run: k, Index: i}
			if tool == "" {
				if s.Kind != trajectory.StepModel || s.Model == nil || s.Model.Class != runrecord.Adopted || len(s.Model.Chosen) == 0 {
					continue
				}
			} else {
				if s.Kind != trajectory.StepTool || s.Tool == nil || s.Tool.Name != tool {
					continue
				}
				unit.Tool = s.Tool.Name
				unit.CallID = s.Tool.ToolCallID
			}
			units = append(units, unit)
		}
	}
	return units
}

// stepPrefix copies only the containers changed for rendering; evidence remains immutable.
func stepPrefix(session trajectory.Session, unit eval.JudgedStep) trajectory.Session {
	prefix := slices.Clone(session[:unit.Run+1])
	run := *prefix[unit.Run]
	run.Steps = slices.Clone(run.Steps[:unit.Index+1])
	last := &run.Steps[unit.Index]
	if last.Tool != nil {
		tool := *last.Tool
		tool.Result = nil
		tool.Error = nil
		tool.Children = nil
		tool.Duration = 0
		last.Tool = &tool
	}
	run.Final = nil
	run.Totals = trajectory.Totals{}
	prefix[unit.Run] = &run
	return prefix
}
