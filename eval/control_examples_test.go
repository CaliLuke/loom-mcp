package eval_test

import (
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

// RegisterExperimentTools binds explicitly granted control tools before runtime sealing.
// snippet:start control-register-tools
func RegisterExperimentTools(rt *runtime.Runtime, cfg control.Config) error {
	service, err := control.New(cfg)
	if err != nil {
		return err
	}
	registration, err := control.NewToolsetRegistration(control.ToolsetConfig{
		Name: "experiment", Service: service,
	})
	if err != nil {
		return err
	}
	return rt.RegisterToolset(registration)
}

// snippet:end control-register-tools
