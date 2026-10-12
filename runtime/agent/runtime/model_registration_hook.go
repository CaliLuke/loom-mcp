package runtime

import (
	"github.com/CaliLuke/loom-mcp/v2/internal/agenttesthook"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// init exposes private construction settings only to the module's test harness.
func init() {
	agenttesthook.SetModelWrapper = func(options any, wrapper func(string, model.Client) model.Client) {
		options.(*Options).modelHook = wrapper
	}
}
