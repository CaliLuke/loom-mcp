package runtime

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

const (
	// PlannerContractErrorType identifies non-retryable planner contract failures.
	PlannerContractErrorType = "planner_contract"
	// ToolResultContractErrorType identifies non-retryable tool-result contract failures.
	ToolResultContractErrorType = "tool_result_contract"
)

// toolResultContractErrorf returns a root application error so engine adapters
// preserve the contract failure's type and non-retryability.
func toolResultContractErrorf(format string, args ...any) *engine.ApplicationError {
	return engine.NewNonRetryableApplicationError(fmt.Sprintf(format, args...), ToolResultContractErrorType, nil, nil)
}
