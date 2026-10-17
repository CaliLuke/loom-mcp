package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StdioOptions configures the stdio-based MCP caller.
type StdioOptions struct {
	Command       string
	Args          []string
	Env           []string
	Dir           string
	ClientName    string
	ClientVersion string
	InitTimeout   time.Duration
	// MaxLineLength limits inbound JSON-RPC frames in bytes. Zero uses the SDK
	// default (16 MiB); a negative value disables the limit.
	MaxLineLength int
	// ElicitationHandler handles server requests for user input. Nil leaves
	// elicitation unsupported. The handler owns user interaction and consent.
	ElicitationHandler func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)
}

// NewStdioCaller launches the target command using CommandTransport,
// performs the MCP initialize handshake via Client.Connect,
// and returns a Caller that wraps the resulting ClientSession.
func NewStdioCaller(ctx context.Context, opts StdioOptions) (*SessionCaller, error) {
	if opts.Command == "" {
		return nil, errors.New("command is required")
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    mcpClientName(opts.ClientName),
		Version: mcpClientVersion(opts.ClientVersion),
	}, &mcp.ClientOptions{ElicitationHandler: opts.ElicitationHandler})

	return connectSession(ctx, opts.InitTimeout, func(sessionCtx context.Context) (*mcp.ClientSession, error) {
		// #nosec G204 -- stdio callers are explicitly configured to launch the target MCP server command.
		cmd := exec.CommandContext(sessionCtx, opts.Command, opts.Args...)
		if opts.Dir != "" {
			cmd.Dir = opts.Dir
		}
		if len(opts.Env) > 0 {
			cmd.Env = opts.Env
		}

		transport := &mcp.CommandTransport{
			Command:       cmd,
			MaxLineLength: opts.MaxLineLength,
		}

		session, err := client.Connect(sessionCtx, transport, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to MCP server: %w", err)
		}
		return session, nil
	})
}
