package mcp

type (
	// Notification describes a server-initiated status update that can be
	// broadcast to connected MCP clients via the Events stream. It carries a
	// machine-usable type, an optional human-readable message, and optional
	// structured data.
	Notification struct {
		Type    string  `json:"type"`
		Message *string `json:"message,omitempty"`
		Data    any     `json:"data,omitempty"`
	}
)
