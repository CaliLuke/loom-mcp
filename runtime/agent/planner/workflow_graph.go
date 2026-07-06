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
	if len(p.nodes) == 0 {
		return nil, errors.New("workflow graph requires at least one node")
	}
	completed := p.completedWorkflowNodes(outputs, typedInputs)
	virtualCompleted := p.completeVirtualNodes(completed, outputs, typedInputs)
	selectedTargets := p.selectedBranchTargets(outputs, typedInputs, completed, virtualCompleted)
	calls, err := p.readyToolCalls(completed, virtualCompleted, selectedTargets, outputs)
	if err != nil {
		return nil, err
	}
	awaitItems, err := p.readyAwaitItems(completed, virtualCompleted, selectedTargets)
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
	return p.finalResult(), nil
}

func (p *GraphWorkflowPlanner) readyToolCalls(completed, virtualCompleted, selectedTargets map[string]struct{}, outputs []*ToolOutput) ([]ToolRequest, error) {
	calls := make([]ToolRequest, 0)
	branchTargets := p.branchTargets()
	for idx, node := range p.nodes {
		if _, isBranchTarget := branchTargets[node.ID]; isBranchTarget {
			if _, selected := selectedTargets[node.ID]; !selected {
				continue
			}
		}
		if !nodeDepsComplete(node.DependsOn, completed, virtualCompleted) {
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

func (p *GraphWorkflowPlanner) readyAwaitItems(completed, virtualCompleted, selectedTargets map[string]struct{}) ([]AwaitItem, error) {
	items := make([]AwaitItem, 0)
	branchTargets := p.branchTargets()
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
		if !nodeDepsComplete(node.DependsOn, completed, virtualCompleted) {
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

func (p *GraphWorkflowPlanner) completeVirtualNodes(completed map[string]struct{}, outputs []*ToolOutput, typedInputs []TypedInputOutput) map[string]struct{} {
	virtualCompleted := make(map[string]struct{})
	changed := true
	for changed {
		changed = false
		for _, node := range p.nodes {
			if node.Kind != WorkflowNodeJoin && node.Kind != WorkflowNodeBranch {
				continue
			}
			if _, ok := virtualCompleted[node.ID]; ok {
				continue
			}
			if node.Kind == WorkflowNodeBranch && selectedBranchTarget(node.Branch, outputs, typedInputs) == "" {
				continue
			}
			if nodeDepsComplete(node.DependsOn, completed, virtualCompleted) {
				virtualCompleted[node.ID] = struct{}{}
				changed = true
			}
		}
	}
	return virtualCompleted
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
		if output == nil || output.ToolCallID == "" {
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
		if node.Branch == nil {
			continue
		}
		for _, branchCase := range node.Branch.Cases {
			targets[branchCase.Target] = struct{}{}
		}
		if node.Branch.Default != "" {
			targets[node.Branch.Default] = struct{}{}
		}
	}
	return targets
}

func (p *GraphWorkflowPlanner) selectedBranchTargets(outputs []*ToolOutput, typedInputs []TypedInputOutput, completed, virtualCompleted map[string]struct{}) map[string]struct{} {
	selected := make(map[string]struct{})
	for _, node := range p.nodes {
		if node.Branch == nil {
			continue
		}
		if !nodeDepsComplete(node.DependsOn, completed, virtualCompleted) {
			continue
		}
		target := selectedBranchTarget(node.Branch, outputs, typedInputs)
		if target != "" {
			selected[target] = struct{}{}
		}
	}
	return selected
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
	for _, output := range outputs {
		if output == nil || output.ToolCallID != step {
			continue
		}
		return output.Result, true
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
