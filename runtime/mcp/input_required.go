package mcp

import (
	"errors"
)

// InputRequiredError marks control-flow errors that ask an MCP transport to
// return an input-required result instead of reporting a tool failure.
type InputRequiredError interface {
	error
	InputRequired()
}

// InvalidClientInputError marks malformed MCP retry state or responses that
// transports must report as protocol errors rather than tool failures.
type InvalidClientInputError interface {
	error
	InvalidClientInput()
}

// IsInputRequired reports whether err asks the active MCP transport to suspend
// the request until the client supplies additional input.
func IsInputRequired(err error) bool {
	var target InputRequiredError
	return errors.As(err, &target)
}

// IsInvalidClientInput reports whether err represents invalid MCP retry input
// supplied by the client.
func IsInvalidClientInput(err error) bool {
	var target InvalidClientInputError
	return errors.As(err, &target)
}
