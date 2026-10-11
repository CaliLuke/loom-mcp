package eval_test

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
)

func ExampleItems() {
	// snippet:start eval-scenarios
	scenario := eval.Scenario{
		ID:    "status",
		Agent: "assistant.assistant_runtime",
		Task:  "Report the current status",
		Items: eval.Items(eval.Item{
			ID:    "ready",
			Input: "What is the status?",
			ExpectedTools: &expect.ExpectedTools{
				Tools: []expect.ExpectedTool{{Name: "projected.projected_status_tool"}},
				Exact: true,
			},
		}),
	}
	// snippet:end eval-scenarios
	fmt.Println(eval.Validate(scenario))
	fmt.Println(scenario.Items.Len())
	// Output:
	// <nil>
	// 1
}
