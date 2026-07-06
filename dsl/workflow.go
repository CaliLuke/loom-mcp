package dsl

import (
	"encoding/json"

	expragents "github.com/CaliLuke/loom-mcp/expr/agent"
	"github.com/CaliLuke/loom/eval"
)

// Workflow configures a generated deterministic workflow planner for the
// current agent.
func Workflow(fn func()) {
	agent, ok := eval.Current().(*expragents.AgentExpr)
	if !ok {
		eval.IncompatibleDSL()
		return
	}
	workflow := agent.Workflow
	if workflow == nil {
		workflow = &expragents.WorkflowExpr{Agent: agent}
		agent.Workflow = workflow
	}
	if fn != nil {
		eval.Execute(fn, workflow)
	}
}

// Step appends one sequential tool call to the current Workflow.
func Step(name string, tool string, payloadJSON string) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		eval.IncompatibleDSL()
		return
	}
	if !json.Valid([]byte(payloadJSON)) {
		eval.ReportError("workflow step %q payload must be valid JSON", name)
		return
	}
	workflow.Steps = append(workflow.Steps, &expragents.WorkflowStepExpr{
		Name:    name,
		Tool:    tool,
		Payload: payloadJSON,
	})
}

// FinalMessage sets the assistant message emitted after all workflow steps complete.
func FinalMessage(message string) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		eval.IncompatibleDSL()
		return
	}
	workflow.FinalMessage = message
}
