package tools

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"

// ResultRefs addresses content committed in the root run scope before a result
// enters workflow state. Tool activities, inline tools, adapted child outputs,
// and externally provided results populate these references. Empty hashes mean
// no corresponding stored content. Error-only and structural results have no
// eligible result reference; Payload is populated only by tool activities.
type ResultRefs struct {
	// Result addresses the full canonical result JSON eligible for model access.
	Result runstore.Hash
	// ServerData addresses the server-only sidecar; never expose it to a model.
	ServerData runstore.Hash
	// Payload addresses the executed payload, including injected server-only values.
	// Neither this hash nor its content may enter model-facing messages.
	Payload runstore.Hash
}
