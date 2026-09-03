package agent

import (
	"encoding/json/jsontext"
	"fmt"
	"strings"

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
		// GraphNodes are executed by the generated graph workflow planner.
		GraphNodes []*WorkflowNodeExpr
		// FinalMessage is emitted after all steps have completed.
		FinalMessage string
		// GraphMode indicates that new Step declarations should become graph nodes.
		GraphMode bool
		// ParallelDepth tracks whether Step declarations are inside Parallel.
		ParallelDepth int
		// CurrentDependsOn carries graph dependencies for subsequent nodes.
		CurrentDependsOn []string
		// BranchTargetDependsOn carries the branch dependency while branch targets are declared.
		BranchTargetDependsOn []string
		// BranchTargetPending tracks target node IDs that should share the branch dependency.
		BranchTargetPending map[string]struct{}
		// BranchTargetEmitted tracks target node IDs declared for the active branch.
		BranchTargetEmitted []string
	}

	// WorkflowNodeKind identifies deterministic graph workflow node behavior.
	WorkflowNodeKind string

	// WorkflowStepExpr captures one sequential workflow tool call.
	WorkflowStepExpr struct {
		// Name identifies the workflow step and becomes the tool call ID.
		Name string
		// Tool is the fully qualified tool identifier.
		Tool string
		// Payload is the raw JSON payload sent to the tool.
		Payload string
	}

	// WorkflowNodeExpr captures one graph workflow node.
	WorkflowNodeExpr struct {
		ID        string
		Kind      WorkflowNodeKind
		Tool      string
		Payload   string
		Title     string
		Schema    string
		DependsOn []string
		Loop      *WorkflowLoopExpr
		Branch    *WorkflowBranchExpr
	}

	// WorkflowLoopExpr captures a bounded repeated workflow tool node.
	WorkflowLoopExpr struct {
		Tool          string
		Payload       string
		MaxIterations int
		Until         *WorkflowPredicateExpr
	}

	// WorkflowBranchExpr captures deterministic graph branch selection.
	WorkflowBranchExpr struct {
		FromStep string
		Cases    []WorkflowBranchCaseExpr
		Default  string
	}

	// WorkflowBranchCaseExpr maps a JSONPath equality predicate to a target node.
	WorkflowBranchCaseExpr struct {
		Path   string
		Equals string
		Target string
	}

	// WorkflowPredicateExpr captures a simple JSONPath equality predicate.
	WorkflowPredicateExpr struct {
		Step   string
		Path   string
		Equals string
	}
)

const (
	// WorkflowNodeTool executes a normal graph tool node.
	WorkflowNodeTool WorkflowNodeKind = "tool"
	// WorkflowNodeParallelStep marks a tool node declared inside Parallel.
	WorkflowNodeParallelStep WorkflowNodeKind = "parallel_step"
	// WorkflowNodeJoin marks a dependency barrier.
	WorkflowNodeJoin WorkflowNodeKind = "join"
	// WorkflowNodeBranch marks a deterministic branch selector.
	WorkflowNodeBranch WorkflowNodeKind = "branch"
	// WorkflowNodeLoop marks a bounded loop node.
	WorkflowNodeLoop WorkflowNodeKind = "loop"
	// WorkflowNodeTypedInput marks a schema-typed human input node.
	WorkflowNodeTypedInput WorkflowNodeKind = "typed_input"
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
	if len(w.Steps) == 0 && len(w.GraphNodes) == 0 {
		verr.Add(w, "workflow requires at least one step")
	}
	if len(w.Steps) > 0 && len(w.GraphNodes) > 0 {
		verr.Add(w, "workflow cannot mix sequential Step declarations with graph constructs")
	}
	w.validateSequential(verr)
	w.validateGraph(verr)
	if len(verr.Errors) == 0 {
		return nil
	}
	return verr
}

func (w *WorkflowExpr) validateGraph(verr *eval.ValidationErrors) {
	ids := make(map[string]struct{}, len(w.GraphNodes))
	nodes := make(map[string]*WorkflowNodeExpr, len(w.GraphNodes))
	for _, node := range w.GraphNodes {
		if node == nil {
			continue
		}
		if node.ID == "" {
			verr.Add(w, "workflow node id is required")
			continue
		}
		if _, ok := ids[node.ID]; ok {
			verr.Add(w, "duplicate workflow node id %q", node.ID)
		}
		ids[node.ID] = struct{}{}
		if _, exists := nodes[node.ID]; !exists {
			nodes[node.ID] = node
		}
	}
	for _, node := range w.GraphNodes {
		if node == nil {
			continue
		}
		w.validateGraphNode(verr, node, ids)
	}
	w.validateGraphCycles(verr, nodes)
}

func (w *WorkflowExpr) validateGraphCycles(verr *eval.ValidationErrors, nodes map[string]*WorkflowNodeExpr) {
	const (
		unvisited uint8 = iota
		visiting
		visited
	)
	state := make(map[string]uint8, len(nodes))
	stack := make([]string, 0, len(nodes))
	positions := make(map[string]int, len(nodes))
	var visit func(string) []string
	visit = func(id string) []string {
		state[id] = visiting
		positions[id] = len(stack)
		stack = append(stack, id)
		for _, dep := range nodes[id].DependsOn {
			if _, exists := nodes[dep]; !exists {
				continue
			}
			switch state[dep] {
			case unvisited:
				if cycle := visit(dep); len(cycle) > 0 {
					return cycle
				}
			case visiting:
				cycle := append([]string(nil), stack[positions[dep]:]...)
				return append(cycle, dep)
			}
		}
		stack = stack[:len(stack)-1]
		delete(positions, id)
		state[id] = visited
		return nil
	}
	for _, node := range w.GraphNodes {
		if node == nil || node.ID == "" || state[node.ID] != unvisited {
			continue
		}
		if cycle := visit(node.ID); len(cycle) > 0 {
			verr.Add(w, "workflow dependency cycle: %s", strings.Join(cycle, " -> "))
			return
		}
	}
}

func (w *WorkflowExpr) validateGraphNode(verr *eval.ValidationErrors, node *WorkflowNodeExpr, ids map[string]struct{}) {
	for _, dep := range node.DependsOn {
		if _, ok := ids[dep]; !ok {
			verr.Add(w, "unresolved workflow dependency %q", dep)
		}
	}
	switch node.Kind {
	case WorkflowNodeTool, WorkflowNodeParallelStep:
		if node.Tool == "" {
			verr.Add(w, "workflow node %q tool is required", node.ID)
		}
		if node.Payload == "" {
			verr.Add(w, "workflow node %q payload is required", node.ID)
		}
	case WorkflowNodeJoin:
		if len(node.DependsOn) == 0 {
			verr.Add(w, "Join %q requires dependencies", node.ID)
		}
	case WorkflowNodeBranch:
		w.validateBranchNode(verr, node, ids)
	case WorkflowNodeLoop:
		w.validateLoopNode(verr, node, ids)
	case WorkflowNodeTypedInput:
		w.validateTypedInputNode(verr, node)
	default:
		verr.Add(w, "unknown workflow node kind %q", node.Kind)
	}
}

func (w *WorkflowExpr) validateTypedInputNode(verr *eval.ValidationErrors, node *WorkflowNodeExpr) {
	if node.Schema == "" {
		verr.Add(w, "RequestInput %q schema is required", node.ID)
		return
	}
	if !jsontext.Value(node.Schema).IsValid() {
		verr.Add(w, "RequestInput %q schema must be valid JSON", node.ID)
	}
}

func (w *WorkflowExpr) validateBranchNode(verr *eval.ValidationErrors, node *WorkflowNodeExpr, ids map[string]struct{}) {
	if node.Branch == nil {
		verr.Add(w, "Branch %q requires branch config", node.ID)
		return
	}
	if node.Branch.FromStep == "" {
		verr.Add(w, "Branch %q requires source step", node.ID)
	} else if _, ok := ids[node.Branch.FromStep]; !ok {
		verr.Add(w, "unresolved branch source step %q", node.Branch.FromStep)
	}
	if node.Branch.Default == "" {
		verr.Add(w, "Branch %q requires Default", node.ID)
	} else if _, ok := ids[node.Branch.Default]; !ok {
		verr.Add(w, "unresolved branch default target %q", node.Branch.Default)
	}
	for _, branchCase := range node.Branch.Cases {
		if branchCase.Path == "" || branchCase.Target == "" {
			verr.Add(w, "Branch %q cases require path and target", node.ID)
			continue
		}
		if _, ok := ids[branchCase.Target]; !ok {
			verr.Add(w, "unresolved branch case target %q", branchCase.Target)
		}
	}
}

func (w *WorkflowExpr) validateLoopNode(verr *eval.ValidationErrors, node *WorkflowNodeExpr, ids map[string]struct{}) {
	if node.Loop == nil {
		verr.Add(w, "Loop %q requires loop config", node.ID)
		return
	}
	if node.Loop.Tool == "" {
		verr.Add(w, "Loop %q tool is required", node.ID)
	}
	if node.Loop.Payload == "" {
		verr.Add(w, "Loop %q payload is required", node.ID)
	}
	if node.Loop.MaxIterations <= 0 {
		verr.Add(w, "Loop requires MaxIterations")
	}
	if node.Loop.Until != nil {
		if node.Loop.Until.Step == "" {
			verr.Add(w, "Loop %q until step is required", node.ID)
		} else if _, ok := ids[node.Loop.Until.Step]; !ok {
			verr.Add(w, "unresolved loop until step %q", node.Loop.Until.Step)
		}
	}
}

func (w *WorkflowExpr) validateSequential(verr *eval.ValidationErrors) {
	names := make(map[string]struct{}, len(w.Steps))
	for _, step := range w.Steps {
		if step == nil {
			continue
		}
		if step.Name == "" {
			verr.Add(w, "workflow step name is required")
		} else {
			if _, ok := names[step.Name]; ok {
				verr.Add(w, "duplicate workflow step name %q", step.Name)
			}
			names[step.Name] = struct{}{}
		}
		if step.Tool == "" {
			verr.Add(w, "workflow step %q tool is required", step.Name)
		}
		if step.Payload == "" {
			verr.Add(w, "workflow step %q payload is required", step.Name)
		}
	}
}
