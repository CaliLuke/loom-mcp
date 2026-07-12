package mcp

import (
	"errors"

	loom "github.com/CaliLuke/loom/pkg"
)

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

	data := &ErrorData{Remedy: loom.ExtractErrorRemedy(err)}
	var (
		serviceError *loom.ServiceError
		namer        loom.LoomErrorNamer
	)
	if errors.As(err, &serviceError) {
		data.Name = serviceError.Name
		data.Temporary = serviceError.Temporary
		data.Timeout = serviceError.Timeout
		data.Fault = serviceError.Fault
	} else if errors.As(err, &namer) {
		data.Name = namer.LoomErrorName()
	}

	if data.Name == "" && !data.Temporary && !data.Timeout && !data.Fault && data.Remedy == nil {
		return nil
	}
	return data
}
