package agent

import (
	"fmt"

	"github.com/CaliLuke/loom/eval"
)

type (
	// WorkflowExpr captures a generated deterministic workflow planner.
	WorkflowExpr struct {
		eval.DSLFunc

		// Agent is the agent this workflow belongs to.
		Agent *AgentExpr
		// Steps are executed sequentially by the generated planner.
		Steps []*WorkflowStepExpr
		// FinalMessage is emitted after all steps have completed.
		FinalMessage string
	}

	// WorkflowStepExpr captures one sequential workflow tool call.
	WorkflowStepExpr struct {
		// Name identifies the workflow step and becomes the tool call ID.
		Name string
		// Tool is the fully qualified tool identifier.
		Tool string
		// Payload is the raw JSON payload sent to the tool.
		Payload string
	}
)

// EvalName returns a descriptive identifier for error reporting.
func (w *WorkflowExpr) EvalName() string {
	if w == nil || w.Agent == nil {
		return "workflow"
	}
	return fmt.Sprintf("workflow for agent %q", w.Agent.Name)
}

// Validate enforces workflow composition invariants.
func (w *WorkflowExpr) Validate() error {
	verr := new(eval.ValidationErrors)
	if len(w.Steps) == 0 {
		verr.Add(w, "workflow requires at least one step")
	}
	for _, step := range w.Steps {
		if step == nil {
			continue
		}
		if step.Name == "" {
			verr.Add(w, "workflow step name is required")
		}
		if step.Tool == "" {
			verr.Add(w, "workflow step %q tool is required", step.Name)
		}
		if step.Payload == "" {
			verr.Add(w, "workflow step %q payload is required", step.Name)
		}
	}
	return verr
}
