package dsl

import (
	"encoding/json/jsontext"

	expragents "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/eval"
)

// Workflow configures a generated deterministic workflow planner for the
// current agent.
func Workflow(fn func()) {
	agent, ok := eval.Current().(*expragents.AgentExpr)
	if !ok {
		incompatibleDSL("Workflow")
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
		incompatibleDSL("Step")
		return
	}
	if !jsontext.Value(payloadJSON).IsValid() {
		eval.ReportError("workflow step %q payload must be valid JSON", name)
		return
	}
	if workflow.GraphMode || workflow.ParallelDepth > 0 || len(workflow.CurrentDependsOn) > 0 {
		kind := expragents.WorkflowNodeTool
		if workflow.ParallelDepth > 0 {
			kind = expragents.WorkflowNodeParallelStep
		}
		deps, branchTarget := workflowStepDependsOn(workflow, name)
		workflow.GraphMode = true
		workflow.GraphNodes = append(workflow.GraphNodes, &expragents.WorkflowNodeExpr{
			ID:        name,
			Kind:      kind,
			Tool:      tool,
			Payload:   payloadJSON,
			DependsOn: deps,
		})
		if workflow.ParallelDepth == 0 {
			advanceWorkflowStepDependsOn(workflow, name, branchTarget)
		}
		return
	}
	workflow.Steps = append(workflow.Steps, &expragents.WorkflowStepExpr{
		Name:    name,
		Tool:    tool,
		Payload: payloadJSON,
	})
}

func workflowStepDependsOn(workflow *expragents.WorkflowExpr, name string) ([]string, bool) {
	if _, ok := workflow.BranchTargetPending[name]; ok {
		return append([]string(nil), workflow.BranchTargetDependsOn...), true
	}
	return append([]string(nil), workflow.CurrentDependsOn...), false
}

func advanceWorkflowStepDependsOn(workflow *expragents.WorkflowExpr, name string, branchTarget bool) {
	if !branchTarget {
		workflow.CurrentDependsOn = []string{name}
		return
	}
	delete(workflow.BranchTargetPending, name)
	workflow.BranchTargetEmitted = append(workflow.BranchTargetEmitted, name)
	if len(workflow.BranchTargetPending) > 0 {
		workflow.CurrentDependsOn = append([]string(nil), workflow.BranchTargetDependsOn...)
		return
	}
	workflow.CurrentDependsOn = append([]string(nil), workflow.BranchTargetEmitted...)
	workflow.BranchTargetDependsOn = nil
	workflow.BranchTargetEmitted = nil
	workflow.BranchTargetPending = nil
}

// FinalMessage sets the assistant message emitted after all workflow steps complete.
func FinalMessage(message string) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		incompatibleDSL("FinalMessage")
		return
	}
	workflow.FinalMessage = message
}

// Parallel declares tool steps that are ready at the same graph frontier.
func Parallel(fn func()) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		incompatibleDSL("Parallel")
		return
	}
	workflow.GraphMode = true
	start := len(workflow.GraphNodes)
	workflow.ParallelDepth++
	if fn != nil {
		eval.Execute(fn, workflow)
	}
	workflow.ParallelDepth--
	ids := make([]string, 0, len(workflow.GraphNodes)-start)
	for _, node := range workflow.GraphNodes[start:] {
		if node != nil {
			ids = append(ids, node.ID)
		}
	}
	workflow.CurrentDependsOn = ids
}

// Join declares a dependency barrier in a workflow graph.
func Join(name string, deps ...string) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		incompatibleDSL("Join")
		return
	}
	workflow.GraphMode = true
	if len(deps) == 0 {
		deps = workflow.CurrentDependsOn
	}
	workflow.GraphNodes = append(workflow.GraphNodes, &expragents.WorkflowNodeExpr{
		ID:        name,
		Kind:      expragents.WorkflowNodeJoin,
		DependsOn: append([]string(nil), deps...),
	})
	workflow.CurrentDependsOn = []string{name}
}

// RequestInput declares a schema-typed human input node in a workflow graph.
func RequestInput(name string, title string, schemaJSON string) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		incompatibleDSL("RequestInput")
		return
	}
	if !jsontext.Value(schemaJSON).IsValid() {
		eval.ReportError("workflow input %q schema must be valid JSON", name)
		return
	}
	workflow.GraphMode = true
	workflow.GraphNodes = append(workflow.GraphNodes, &expragents.WorkflowNodeExpr{
		ID:        name,
		Kind:      expragents.WorkflowNodeTypedInput,
		Title:     title,
		Schema:    schemaJSON,
		DependsOn: append([]string(nil), workflow.CurrentDependsOn...),
	})
	if workflow.ParallelDepth == 0 {
		workflow.CurrentDependsOn = []string{name}
	}
}

// LoopOption configures Loop nodes.
type LoopOption func(*expragents.WorkflowLoopExpr)

// BranchOption configures Branch nodes.
type BranchOption func(*expragents.WorkflowBranchExpr)

// Loop declares a bounded repeated tool node in a workflow graph.
func Loop(name string, tool string, payloadJSON string, opts ...LoopOption) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		incompatibleDSL("Loop")
		return
	}
	if !jsontext.Value(payloadJSON).IsValid() {
		eval.ReportError("workflow loop %q payload must be valid JSON", name)
		return
	}
	loop := &expragents.WorkflowLoopExpr{Tool: tool, Payload: payloadJSON}
	for _, opt := range opts {
		if opt != nil {
			opt(loop)
		}
	}
	workflow.GraphMode = true
	workflow.GraphNodes = append(workflow.GraphNodes, &expragents.WorkflowNodeExpr{
		ID:        name,
		Kind:      expragents.WorkflowNodeLoop,
		DependsOn: append([]string(nil), workflow.CurrentDependsOn...),
		Loop:      loop,
	})
	workflow.CurrentDependsOn = []string{name}
}

// MaxIterations bounds Loop execution.
func MaxIterations(n int) LoopOption {
	return func(loop *expragents.WorkflowLoopExpr) {
		loop.MaxIterations = n
	}
}

// UntilJSONPath stops Loop when a prior step JSON value equals the expected string.
func UntilJSONPath(step, path, equals string) LoopOption {
	return func(loop *expragents.WorkflowLoopExpr) {
		loop.Until = &expragents.WorkflowPredicateExpr{Step: step, Path: path, Equals: equals}
	}
}

// Branch declares a deterministic branch selector.
func Branch(name string, fromStep string, opts ...BranchOption) {
	workflow, ok := eval.Current().(*expragents.WorkflowExpr)
	if !ok {
		incompatibleDSL("Branch")
		return
	}
	branch := &expragents.WorkflowBranchExpr{FromStep: fromStep}
	for _, opt := range opts {
		if opt != nil {
			opt(branch)
		}
	}
	workflow.GraphMode = true
	deps := append([]string(nil), workflow.CurrentDependsOn...)
	if len(deps) == 0 && fromStep != "" {
		deps = []string{fromStep}
	}
	workflow.GraphNodes = append(workflow.GraphNodes, &expragents.WorkflowNodeExpr{
		ID:        name,
		Kind:      expragents.WorkflowNodeBranch,
		DependsOn: deps,
		Branch:    branch,
	})
	workflow.CurrentDependsOn = []string{name}
	targets := map[string]struct{}{}
	for _, branchCase := range branch.Cases {
		if branchCase.Target != "" {
			targets[branchCase.Target] = struct{}{}
		}
	}
	if branch.Default != "" {
		targets[branch.Default] = struct{}{}
	}
	if len(targets) > 0 {
		workflow.BranchTargetDependsOn = []string{name}
		workflow.BranchTargetPending = targets
		workflow.BranchTargetEmitted = nil
	}
}

// Case maps a JSONPath equality predicate to a branch target node.
func Case(path, equals, target string) BranchOption {
	return func(branch *expragents.WorkflowBranchExpr) {
		branch.Cases = append(branch.Cases, expragents.WorkflowBranchCaseExpr{
			Path:   path,
			Equals: equals,
			Target: target,
		})
	}
}

// BranchDefault sets the branch target used when no Case matches.
func BranchDefault(target string) BranchOption {
	return func(branch *expragents.WorkflowBranchExpr) {
		branch.Default = target
	}
}
