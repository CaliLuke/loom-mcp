package tools

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"

// ResultRefs addresses content written by a tool activity in the root run scope.
// Empty hashes mean that the corresponding content was not stored. Inline,
// child-agent, and externally provided results do not populate these references.
type ResultRefs struct {
	// Result addresses the full canonical result JSON eligible for model access.
	Result runstore.Hash
	// ServerData addresses the server-only sidecar; never expose it to a model.
	ServerData runstore.Hash
	// Payload addresses the executed payload, including injected server-only values.
	// Neither this hash nor its content may enter model-facing messages.
	Payload runstore.Hash
}
