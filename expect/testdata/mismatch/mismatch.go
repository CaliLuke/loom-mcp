package mismatch

import (
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// EchoPayload is the payload required by EchoTool.
type EchoPayload struct{ Text string }

// EchoResult is EchoTool's result.
type EchoResult struct{ Text string }

// Other deliberately does not match EchoPayload.
type Other struct{ Number int }

// EchoTool requires an EchoPayload predicate.
var EchoTool = tools.TypedTool[*EchoPayload, *EchoResult]{Name: "echo.echo"}

func mismatch() {
	expect.Call(EchoTool, func(*Other) error {
		return nil
	}, nil)
}
