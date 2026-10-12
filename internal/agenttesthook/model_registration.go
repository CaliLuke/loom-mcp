// Package agenttesthook connects agenttest to private runtime construction hooks.
package agenttesthook

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"

// SetModelWrapper is initialized by runtime before agenttest constructs a runtime.
// options must be a *runtime.Options. The opaque argument avoids an import cycle;
// applications outside this module cannot import this package.
var SetModelWrapper func(options any, wrapper func(string, model.Client) model.Client)
