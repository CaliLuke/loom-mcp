package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// WorkflowNodeKind identifies graph workflow node behavior.
	WorkflowNodeKind string

	// WorkflowGraphConfig configures a deterministic graph workflow planner.
	WorkflowGraphConfig struct {
		Nodes        []WorkflowNode
		FinalMessage string
	}

	// WorkflowNode describes one deterministic workflow graph node.
	WorkflowNode struct {
		ID        string
		Kind      WorkflowNodeKind
		Tool      tools.Ident
		Payload   rawjson.Message
		Title     string
		Schema    rawjson.Message
		DependsOn []string
		Loop      *WorkflowLoopConfig
		Branch    *WorkflowBranchConfig
	}

	// WorkflowLoopConfig describes a bounded repeated tool node.
	WorkflowLoopConfig struct {
		Tool          tools.Ident
		Payload       rawjson.Message
		MaxIterations int
		Until         *WorkflowPredicateConfig
	}

	// WorkflowBranchConfig describes a deterministic branch selection.
	WorkflowBranchConfig struct {
		FromStep string
		Cases    []WorkflowBranchCase
		Default  string
	}

	// WorkflowBranchCase maps a JSONPath equality predicate to a target node.
	WorkflowBranchCase struct {
		Path   string
		Equals string
		Target string
	}

	// WorkflowPredicateConfig captures a simple JSONPath equality predicate.
	WorkflowPredicateConfig struct {
		Step   string
		Path   string
		Equals string
	}

	// GraphWorkflowPlanner runs deterministic workflow graph nodes.
	GraphWorkflowPlanner struct {
		nodes        []WorkflowNode
		finalMessage string
	}
)

const (
	// WorkflowNodeTool executes one tool after dependencies complete.
	WorkflowNodeTool WorkflowNodeKind = "tool"
	// WorkflowNodeJoin marks a dependency barrier. It does not emit a tool call.
	WorkflowNodeJoin WorkflowNodeKind = "join"
	// WorkflowNodeBranch marks a deterministic branch selector.
	WorkflowNodeBranch WorkflowNodeKind = "branch"
	// WorkflowNodeLoop executes a bounded repeated tool.
	WorkflowNodeLoop WorkflowNodeKind = "loop"
	// WorkflowNodeTypedInput requests schema-typed human input.
	WorkflowNodeTypedInput WorkflowNodeKind = "typed_input"
)

// NewGraphWorkflowPlanner constructs a deterministic graph workflow planner.
func NewGraphWorkflowPlanner(cfg WorkflowGraphConfig) *GraphWorkflowPlanner {
	return &GraphWorkflowPlanner{
		nodes:        append([]WorkflowNode(nil), cfg.Nodes...),
		finalMessage: cfg.FinalMessage,
	}
}

// PlanStart emits all initially ready workflow graph tool calls.
func (p *GraphWorkflowPlanner) PlanStart(_ context.Context, _ *PlanInput) (*PlanResult, error) {
	return p.nextResult(nil, nil)
}

// PlanResume emits ready graph nodes without rerunning completed nodes.
func (p *GraphWorkflowPlanner) PlanResume(_ context.Context, input *PlanResumeInput) (*PlanResult, error) {
	if input == nil {
		return nil, errors.New("plan resume input is required")
	}
	return p.nextResult(input.ToolOutputs, input.TypedInputs)
}

func (p *GraphWorkflowPlanner) nextResult(outputs []*ToolOutput, typedInputs []TypedInputOutput) (*PlanResult, error) {
	if err := p.validateConfig(); err != nil {
		return nil, err
	}
	if failed := p.firstFailedNonLoopToolOutput(outputs); failed != nil {
		nodeID := graphNodeIDForToolCallID(failed.ToolCallID)
		return nil, fmt.Errorf("workflow node %q failed at %q: %s", nodeID, failed.ToolCallID, failed.Error.Error())
	}
	completed := p.completedWorkflowNodes(outputs, typedInputs)
	virtualCompleted, selectedTargets, skippedTargets := p.graphProgress(completed, outputs, typedInputs)
	calls, err := p.readyToolCalls(completed, virtualCompleted, selectedTargets, skippedTargets, outputs)
	if err != nil {
		return nil, err
	}
	awaitItems, err := p.readyAwaitItems(completed, virtualCompleted, selectedTargets, skippedTargets)
	if err != nil {
		return nil, err
	}
	if len(calls) > 0 || len(awaitItems) > 0 {
		result := &PlanResult{ToolCalls: calls}
		if len(awaitItems) > 0 {
			result.Await = &Await{Items: awaitItems}
		}
		return result, nil
	}
	if incomplete := p.incompleteRequiredNodes(completed, virtualCompleted, selectedTargets, skippedTargets); len(incomplete) > 0 {
		return nil, fmt.Errorf("workflow graph stuck; incomplete nodes: %s", strings.Join(incomplete, ", "))
	}
	return p.finalResult(), nil
}

func (p *GraphWorkflowPlanner) readyToolCalls(completed, virtualCompleted, selectedTargets, skippedTargets map[string]struct{}, outputs []*ToolOutput) ([]ToolRequest, error) {
	calls := make([]ToolRequest, 0)
	branchTargets := p.branchTargets()
	virtualOrSkipped := mergeNodeSets(virtualCompleted, skippedTargets)
	for idx, node := range p.nodes {
		if _, isBranchTarget := branchTargets[node.ID]; isBranchTarget {
			if _, selected := selectedTargets[node.ID]; !selected {
				continue
			}
		}
		if !nodeDepsComplete(node.DependsOn, completed, virtualOrSkipped) {
			continue
		}
		switch node.Kind {
		case WorkflowNodeTool, "":
			if _, ok := completed[node.ID]; ok {
				continue
			}
			call, err := graphToolRequest(node, node.ID, node.Tool, node.Payload, idx)
			if err != nil {
				return nil, err
			}
			calls = append(calls, call)
		case WorkflowNodeLoop:
			call, ok, err := nextLoopCall(node, outputs)
			if err != nil {
				return nil, err
			}
			if ok {
				calls = append(calls, call)
			}
		case WorkflowNodeJoin, WorkflowNodeBranch, WorkflowNodeTypedInput:
			continue
		default:
			return nil, fmt.Errorf("unknown workflow node kind %q", node.Kind)
		}
	}
	return calls, nil
}

func (p *GraphWorkflowPlanner) readyAwaitItems(completed, virtualCompleted, selectedTargets, skippedTargets map[string]struct{}) ([]AwaitItem, error) {
	items := make([]AwaitItem, 0)
	branchTargets := p.branchTargets()
	virtualOrSkipped := mergeNodeSets(virtualCompleted, skippedTargets)
	for _, node := range p.nodes {
		if node.Kind != WorkflowNodeTypedInput {
			continue
		}
		if _, isBranchTarget := branchTargets[node.ID]; isBranchTarget {
			if _, selected := selectedTargets[node.ID]; !selected {
				continue
			}
		}
		if _, ok := completed[node.ID]; ok {
			continue
		}
		if !nodeDepsComplete(node.DependsOn, completed, virtualOrSkipped) {
			continue
		}
		if node.ID == "" {
			return nil, errors.New("workflow typed input requires id")
		}
		if len(node.Schema) == 0 {
			return nil, fmt.Errorf("workflow typed input %q requires schema", node.ID)
		}
		items = append(items, AwaitTypedInputItem(&AwaitTypedInput{
			ID:     node.ID,
			Title:  node.Title,
			Schema: node.Schema,
		}))
	}
	return items, nil
}

func (p *GraphWorkflowPlanner) graphProgress(completed map[string]struct{}, outputs []*ToolOutput, typedInputs []TypedInputOutput) (map[string]struct{}, map[string]struct{}, map[string]struct{}) {
	virtualCompleted := make(map[string]struct{})
	selectedTargets := make(map[string]struct{})
	skippedTargets := make(map[string]struct{})
	for {
		p.resolveVirtualNodes(completed, outputs, typedInputs, virtualCompleted, selectedTargets, skippedTargets)
		if !p.propagateSkippedBranchTargets(selectedTargets, skippedTargets) {
			return virtualCompleted, selectedTargets, skippedTargets
		}
	}
}

// resolveVirtualNodes advances join and branch nodes to a fixpoint. Nodes in
// skippedTargets sit on unselected branch paths and never resolve, so a
// skipped branch cannot select or schedule its own targets. A later selection
// of a skipped node removes it from skippedTargets and resolves it normally.
func (p *GraphWorkflowPlanner) resolveVirtualNodes(completed map[string]struct{}, outputs []*ToolOutput, typedInputs []TypedInputOutput, virtualCompleted, selectedTargets, skippedTargets map[string]struct{}) {
	changed := true
	for changed {
		changed = false
		virtualOrSkipped := mergeNodeSets(virtualCompleted, skippedTargets)
		for _, node := range p.nodes {
			if node.Kind != WorkflowNodeJoin && node.Kind != WorkflowNodeBranch {
				continue
			}
			if _, ok := virtualCompleted[node.ID]; ok {
				continue
			}
			if _, ok := skippedTargets[node.ID]; ok {
				continue
			}
			if !nodeDepsComplete(node.DependsOn, completed, virtualOrSkipped) {
				continue
			}
			if node.Kind == WorkflowNodeBranch {
				target := selectedBranchTarget(node.Branch, outputs, typedInputs)
				if target == "" {
					continue
				}
				if _, ok := selectedTargets[target]; !ok {
					selectedTargets[target] = struct{}{}
					delete(skippedTargets, target)
				}
				for branchTarget := range branchConfigTargets(node.Branch) {
					if branchTarget == target {
						continue
					}
					if _, selected := selectedTargets[branchTarget]; selected {
						continue
					}
					skippedTargets[branchTarget] = struct{}{}
				}
			}
			virtualCompleted[node.ID] = struct{}{}
			changed = true
		}
	}
}

// propagateSkippedBranchTargets marks every target of a skipped branch node as
// skipped so entire unselected paths stay inert, and reports whether any new
// node was skipped. Targets selected by another branch are never skipped: a
// node reachable from both a selected and a skipped path still runs.
func (p *GraphWorkflowPlanner) propagateSkippedBranchTargets(selectedTargets, skippedTargets map[string]struct{}) bool {
	changed := false
	progress := true
	for progress {
		progress = false
		for _, node := range p.nodes {
			if node.Kind != WorkflowNodeBranch {
				continue
			}
			if _, skipped := skippedTargets[node.ID]; !skipped {
				continue
			}
			for target := range branchConfigTargets(node.Branch) {
				if _, selected := selectedTargets[target]; selected {
					continue
				}
				if _, alreadySkipped := skippedTargets[target]; alreadySkipped {
					continue
				}
				skippedTargets[target] = struct{}{}
				progress = true
				changed = true
			}
		}
	}
	return changed
}

func (p *GraphWorkflowPlanner) finalResult() *PlanResult {
	return &PlanResult{
		FinalResponse: &FinalResponse{
			Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: p.finalMessage}},
			},
		},
	}
}

func (p *GraphWorkflowPlanner) completedWorkflowNodes(outputs []*ToolOutput, typedInputs []TypedInputOutput) map[string]struct{} {
	completed := make(map[string]struct{}, len(outputs))
	for _, output := range outputs {
		if output == nil || output.ToolCallID == "" || output.Error != nil {
			continue
		}
		completed[output.ToolCallID] = struct{}{}
		if base, _, ok := strings.Cut(output.ToolCallID, "#"); ok && p.loopDone(base, outputs) {
			completed[base] = struct{}{}
		}
	}
	for _, input := range typedInputs {
		if input.ID != "" {
			completed[input.ID] = struct{}{}
		}
	}
	return completed
}

func (p *GraphWorkflowPlanner) loopDone(nodeID string, outputs []*ToolOutput) bool {
	for _, node := range p.nodes {
		if node.ID == nodeID && node.Kind == WorkflowNodeLoop {
			return node.Loop != nil && (loopSatisfied(node.Loop, outputs) || loopIterationCount(node.ID, outputs) >= node.Loop.MaxIterations)
		}
	}
	return false
}

func (p *GraphWorkflowPlanner) firstFailedNonLoopToolOutput(outputs []*ToolOutput) *ToolOutput {
	for _, output := range outputs {
		if output == nil || output.Error == nil {
			continue
		}
		if p.isLoopToolCallID(output.ToolCallID) {
			continue
		}
		return output
	}
	return nil
}

func (p *GraphWorkflowPlanner) isLoopToolCallID(toolCallID string) bool {
	nodeID, _, ok := strings.Cut(toolCallID, "#")
	if !ok {
		return false
	}
	for _, node := range p.nodes {
		if node.ID == nodeID && node.Kind == WorkflowNodeLoop {
			return true
		}
	}
	return false
}

func (p *GraphWorkflowPlanner) incompleteRequiredNodes(completed, virtualCompleted, selectedTargets, skippedTargets map[string]struct{}) []string {
	incomplete := make([]string, 0)
	branchTargets := p.branchTargets()
	for _, node := range p.nodes {
		if _, skipped := skippedTargets[node.ID]; skipped {
			continue
		}
		if _, isBranchTarget := branchTargets[node.ID]; isBranchTarget {
			if _, selected := selectedTargets[node.ID]; !selected {
				continue
			}
		}
		if _, ok := completed[node.ID]; ok {
			continue
		}
		if _, ok := virtualCompleted[node.ID]; ok {
			continue
		}
		incomplete = append(incomplete, node.ID)
	}
	return incomplete
}

func nodeDepsComplete(deps []string, completed, virtualCompleted map[string]struct{}) bool {
	for _, dep := range deps {
		if _, ok := completed[dep]; ok {
			continue
		}
		if _, ok := virtualCompleted[dep]; ok {
			continue
		}
		return false
	}
	return true
}

func graphToolRequest(node WorkflowNode, id string, tool tools.Ident, payload rawjson.Message, index int) (ToolRequest, error) {
	if tool == "" {
		return ToolRequest{}, fmt.Errorf("workflow node %q requires tool", node.ID)
	}
	if id == "" {
		id = fmt.Sprintf("node-%d", index+1)
	}
	return ToolRequest{Name: tool, Payload: payload, ToolCallID: id}, nil
}

func nextLoopCall(node WorkflowNode, outputs []*ToolOutput) (ToolRequest, bool, error) {
	if node.Loop == nil {
		return ToolRequest{}, false, fmt.Errorf("workflow loop %q requires loop config", node.ID)
	}
	if node.Loop.MaxIterations <= 0 {
		return ToolRequest{}, false, fmt.Errorf("workflow loop %q requires MaxIterations", node.ID)
	}
	if loopSatisfied(node.Loop, outputs) {
		return ToolRequest{}, false, nil
	}
	done := loopIterationCount(node.ID, outputs)
	if done >= node.Loop.MaxIterations {
		return ToolRequest{}, false, nil
	}
	id := fmt.Sprintf("%s#%d", node.ID, done+1)
	call, err := graphToolRequest(node, id, node.Loop.Tool, node.Loop.Payload, done)
	return call, err == nil, err
}

func (p *GraphWorkflowPlanner) branchTargets() map[string]struct{} {
	targets := make(map[string]struct{})
	for _, node := range p.nodes {
		for target := range branchConfigTargets(node.Branch) {
			targets[target] = struct{}{}
		}
	}
	return targets
}

func branchConfigTargets(branch *WorkflowBranchConfig) map[string]struct{} {
	targets := make(map[string]struct{})
	if branch == nil {
		return targets
	}
	for _, branchCase := range branch.Cases {
		if branchCase.Target != "" {
			targets[branchCase.Target] = struct{}{}
		}
	}
	if branch.Default != "" {
		targets[branch.Default] = struct{}{}
	}
	return targets
}

func mergeNodeSets(sets ...map[string]struct{}) map[string]struct{} {
	merged := make(map[string]struct{})
	for _, set := range sets {
		for id := range set {
			merged[id] = struct{}{}
		}
	}
	return merged
}

func selectedBranchTarget(branch *WorkflowBranchConfig, outputs []*ToolOutput, typedInputs []TypedInputOutput) string {
	if branch == nil {
		return ""
	}
	raw, ok := workflowNodeResult(branch.FromStep, outputs, typedInputs)
	if !ok {
		return ""
	}
	for _, branchCase := range branch.Cases {
		if jsonPathEquals(raw, branchCase.Path, branchCase.Equals) {
			return branchCase.Target
		}
	}
	return branch.Default
}

func loopSatisfied(loop *WorkflowLoopConfig, outputs []*ToolOutput) bool {
	if loop == nil || loop.Until == nil {
		return false
	}
	raw, ok := outputResult(loop.Until.Step, outputs)
	if !ok {
		return false
	}
	return jsonPathEquals(raw, loop.Until.Path, loop.Until.Equals)
}

func outputResult(step string, outputs []*ToolOutput) (rawjson.Message, bool) {
	var loopResult rawjson.Message
	loopPrefix := step + "#"
	for _, output := range outputs {
		if output == nil || output.Error != nil {
			continue
		}
		if output.ToolCallID == step {
			return output.Result, true
		}
		if strings.HasPrefix(output.ToolCallID, loopPrefix) {
			loopResult = output.Result
		}
	}
	if loopResult != nil {
		return loopResult, true
	}
	return nil, false
}

func workflowNodeResult(step string, outputs []*ToolOutput, typedInputs []TypedInputOutput) (rawjson.Message, bool) {
	if raw, ok := outputResult(step, outputs); ok {
		return raw, true
	}
	for _, input := range typedInputs {
		if input.ID == step {
			return input.Payload, true
		}
	}
	return nil, false
}

func loopIterationCount(nodeID string, outputs []*ToolOutput) int {
	count := 0
	prefix := nodeID + "#"
	for _, output := range outputs {
		if output == nil {
			continue
		}
		if strings.HasPrefix(output.ToolCallID, prefix) {
			count++
		}
	}
	return count
}

func jsonPathEquals(raw rawjson.Message, path, equals string) bool {
	if !strings.HasPrefix(path, "$.") {
		return false
	}
	var data map[string]any
	if err := json.Unmarshal(raw.RawMessage(), &data); err != nil {
		return false
	}
	value, ok := data[strings.TrimPrefix(path, "$.")]
	return ok && fmt.Sprint(value) == equals
}

func graphNodeIDForToolCallID(toolCallID string) string {
	if base, _, ok := strings.Cut(toolCallID, "#"); ok {
		return base
	}
	return toolCallID
}

func (p *GraphWorkflowPlanner) validateConfig() error {
	if len(p.nodes) == 0 {
		return errors.New("workflow graph requires at least one node")
	}
	ids := make(map[string]struct{}, len(p.nodes))
	for _, node := range p.nodes {
		if node.ID == "" {
			return errors.New("workflow graph node id is required")
		}
		if _, exists := ids[node.ID]; exists {
			return fmt.Errorf("duplicate workflow graph node id %q", node.ID)
		}
		ids[node.ID] = struct{}{}
	}
	for _, node := range p.nodes {
		if err := validateGraphNodeConfig(node, ids); err != nil {
			return err
		}
	}
	return nil
}

func validateGraphNodeConfig(node WorkflowNode, ids map[string]struct{}) error {
	for _, dep := range node.DependsOn {
		if _, ok := ids[dep]; !ok {
			return fmt.Errorf("workflow node %q dependency %q does not exist", node.ID, dep)
		}
	}
	switch node.Kind {
	case WorkflowNodeTool, "":
		if node.Tool == "" {
			return fmt.Errorf("workflow node %q requires tool", node.ID)
		}
	case WorkflowNodeJoin:
		return nil
	case WorkflowNodeBranch:
		return validateGraphBranchConfig(node, ids)
	case WorkflowNodeLoop:
		return validateGraphLoopConfig(node, ids)
	case WorkflowNodeTypedInput:
		if len(node.Schema) == 0 {
			return fmt.Errorf("workflow typed input %q requires schema", node.ID)
		}
	default:
		return fmt.Errorf("unknown workflow node kind %q", node.Kind)
	}
	return nil
}

func validateGraphBranchConfig(node WorkflowNode, ids map[string]struct{}) error {
	if node.Branch == nil {
		return fmt.Errorf("workflow branch %q requires branch config", node.ID)
	}
	if node.Branch.FromStep == "" {
		return fmt.Errorf("workflow branch %q requires fromStep", node.ID)
	}
	if _, ok := ids[node.Branch.FromStep]; !ok {
		return fmt.Errorf("workflow branch %q fromStep %q does not exist", node.ID, node.Branch.FromStep)
	}
	if node.Branch.Default == "" {
		return fmt.Errorf("workflow branch %q requires default target", node.ID)
	}
	if _, ok := ids[node.Branch.Default]; !ok {
		return fmt.Errorf("workflow branch %q default target %q does not exist", node.ID, node.Branch.Default)
	}
	for _, branchCase := range node.Branch.Cases {
		if branchCase.Target == "" {
			return fmt.Errorf("workflow branch %q case target is required", node.ID)
		}
		if _, ok := ids[branchCase.Target]; !ok {
			return fmt.Errorf("workflow branch %q case target %q does not exist", node.ID, branchCase.Target)
		}
		if err := validateWorkflowJSONPath(branchCase.Path); err != nil {
			return fmt.Errorf("workflow branch %q case path %q is unsupported: %w", node.ID, branchCase.Path, err)
		}
	}
	return nil
}

func validateGraphLoopConfig(node WorkflowNode, ids map[string]struct{}) error {
	if node.Loop == nil {
		return fmt.Errorf("workflow loop %q requires loop config", node.ID)
	}
	if node.Loop.Tool == "" {
		return fmt.Errorf("workflow loop %q requires tool", node.ID)
	}
	if node.Loop.MaxIterations <= 0 {
		return fmt.Errorf("workflow loop %q requires MaxIterations", node.ID)
	}
	if node.Loop.Until == nil {
		return nil
	}
	if node.Loop.Until.Step == "" {
		return fmt.Errorf("workflow loop %q until step is required", node.ID)
	}
	if _, ok := ids[node.Loop.Until.Step]; !ok {
		return fmt.Errorf("workflow loop %q until step %q does not exist", node.ID, node.Loop.Until.Step)
	}
	if err := validateWorkflowJSONPath(node.Loop.Until.Path); err != nil {
		return fmt.Errorf("workflow loop %q until path %q is unsupported: %w", node.ID, node.Loop.Until.Path, err)
	}
	return nil
}

func validateWorkflowJSONPath(path string) error {
	if !strings.HasPrefix(path, "$.") {
		return errors.New("expected $.field")
	}
	field := strings.TrimPrefix(path, "$.")
	if field == "" {
		return errors.New("field is required")
	}
	for idx, r := range field {
		if isWorkflowJSONPathLetter(r) || r == '_' {
			continue
		}
		if idx > 0 && r >= '0' && r <= '9' {
			continue
		}
		return errors.New("only top-level identifier fields are supported")
	}
	return nil
}

func isWorkflowJSONPathLetter(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}
