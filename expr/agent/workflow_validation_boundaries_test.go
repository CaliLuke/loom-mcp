package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkflowValidationExplainsIncompleteGraphDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		node       *WorkflowNodeExpr
	}{
		{"id", "node id is required", &WorkflowNodeExpr{Kind: WorkflowNodeTool, Tool: "read", Payload: "{}"}},
		{"tool", "tool is required", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeTool, Payload: "{}"}},
		{"payload", "payload is required", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeTool, Tool: "read"}},
		{"join", "requires dependencies", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeJoin}},
		{"unknown kind", "unknown workflow node kind", &WorkflowNodeExpr{ID: "work", Kind: "future"}},
		{"input schema", "schema is required", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeTypedInput}},
		{"branch config", "requires branch config", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeBranch}},
		{"branch source", "requires source step", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchExpr{Default: "target"}}},
		{"missing source", "unresolved branch source", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchExpr{FromStep: "absent", Default: "target"}}},
		{"branch default", "requires Default", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchExpr{FromStep: "source"}}},
		{"missing default", "unresolved branch default", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchExpr{FromStep: "source", Default: "absent"}}},
		{"empty case", "cases require path and target", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchExpr{FromStep: "source", Default: "target", Cases: []WorkflowBranchCaseExpr{{Path: "$.ok"}}}}},
		{"missing case", "unresolved branch case", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeBranch, Branch: &WorkflowBranchExpr{FromStep: "source", Default: "target", Cases: []WorkflowBranchCaseExpr{{Path: "$.ok", Target: "absent"}}}}},
		{"loop config", "requires loop config", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeLoop}},
		{"loop tool", "tool is required", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Payload: "{}", MaxIterations: 1}}},
		{"loop payload", "payload is required", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "read", MaxIterations: 1}}},
		{"loop predicate", "until step is required", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "read", Payload: "{}", MaxIterations: 1, Until: &WorkflowPredicateExpr{Path: "$.ok"}}}},
		{"missing predicate source", "unresolved loop until step", &WorkflowNodeExpr{ID: "work", Kind: WorkflowNodeLoop, Loop: &WorkflowLoopExpr{Tool: "read", Payload: "{}", MaxIterations: 1, Until: &WorkflowPredicateExpr{Step: "absent", Path: "$.ok"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := &WorkflowExpr{GraphNodes: []*WorkflowNodeExpr{nil, {ID: "source", Kind: WorkflowNodeTool, Tool: "read", Payload: "{}"}, {ID: "target", Kind: WorkflowNodeTool, Tool: "save", Payload: "{}"}, tc.node}}
			require.ErrorContains(t, workflow.Validate(), tc.want)
		})
	}
}

func TestWorkflowValidationExplainsIncompleteSequentialDeclarations(t *testing.T) {
	require.ErrorContains(t, (&WorkflowExpr{}).Validate(), "at least one step")
	for _, tc := range []struct {
		name, want string
		step       *WorkflowStepExpr
	}{
		{"name", "step name is required", &WorkflowStepExpr{Tool: "read", Payload: "{}"}},
		{"tool", "tool is required", &WorkflowStepExpr{Name: "read", Payload: "{}"}},
		{"payload", "payload is required", &WorkflowStepExpr{Name: "read", Tool: "read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, (&WorkflowExpr{Steps: []*WorkflowStepExpr{nil, tc.step}}).Validate(), tc.want)
		})
	}
}
