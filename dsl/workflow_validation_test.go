package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

func TestWorkflowDSLRejectsInvalidJSON(t *testing.T) {
	cases := []struct {
		name string
		node func()
		want string
	}{
		{
			name: "step payload",
			node: func() { Step("search", "tools.search", "{") },
			want: "workflow step \"search\" payload must be valid JSON",
		},
		{
			name: "input schema",
			node: func() { RequestInput("approval", "Approval", "{") },
			want: "workflow input \"approval\" schema must be valid JSON",
		},
		{
			name: "loop payload",
			node: func() { Loop("retry", "tools.retry", "{", MaxIterations(2)) },
			want: "workflow loop \"retry\" payload must be valid JSON",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDSLExpectError(t, workflowDesign(tc.node), tc.want)
		})
	}
}

func TestWorkflowDSLRejectsDuplicateSequentialStepNames(t *testing.T) {
	runDSLExpectError(t, workflowDesign(func() {
		Step("search", "tools.search", `{}`)
		Step("search", "tools.refine", `{}`)
	}), `duplicate workflow step name "search"`)
}

func workflowDesign(node func()) func() {
	return func() {
		API("test", func() {})
		Service("svc", func() {
			Agent("agent", "desc", func() {
				Workflow(node)
			})
		})
	}
}
