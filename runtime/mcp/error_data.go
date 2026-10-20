package mcp

import loom "github.com/CaliLuke/loom/pkg"

type (
	// ErrorData is the client-safe metadata included with MCP JSON-RPC errors.
	// It intentionally omits Loom service error instance IDs.
	ErrorData struct {
		Name      string            `json:"name,omitempty"`
		Temporary bool              `json:"temporary,omitempty"`
		Timeout   bool              `json:"timeout,omitempty"`
		Fault     bool              `json:"fault,omitempty"`
		Remedy    *loom.ErrorRemedy `json:"remedy,omitempty"`
	}
)

// NewErrorData returns client-safe MCP JSON-RPC metadata for err.
func NewErrorData(err error) any {
	if err == nil {
		return nil
	}

	response := loom.ResolveErrorResponse(err)
	data := &ErrorData{Name: response.Name, Remedy: response.Remedy}
	// The selected owner supplies its own traits, never those of a nested cause.
	//nolint:errorlint // ResolveErrorResponse already selected the concrete owner.
	if serviceError, ok := response.Owner.(*loom.ServiceError); ok {
		data.Temporary = serviceError.Temporary
		data.Timeout = serviceError.Timeout
		data.Fault = serviceError.Fault
	}

	if data.Name == "" && !data.Temporary && !data.Timeout && !data.Fault && data.Remedy == nil {
		return nil
	}
	return data
}
