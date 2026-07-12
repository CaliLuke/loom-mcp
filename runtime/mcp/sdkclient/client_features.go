// Package sdkclient adapts an official MCP SDK server session to loom-mcp's
// transport-neutral server-to-client runtime contracts.
package sdkclient

import (
	"context"
	"fmt"

	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WithClientFeatures stores all supported SDK-backed client feature adapters in
// ctx. A nil session leaves ctx unchanged.
func WithClientFeatures(ctx context.Context, session *mcp.ServerSession) context.Context {
	if session == nil {
		return ctx
	}
	ctx = mcpruntime.WithElicitor(ctx, sessionElicitor{session: session})
	ctx = mcpruntime.WithSampler(ctx, sessionSampler{session: session})
	ctx = mcpruntime.WithRootLister(ctx, sessionRootLister{session: session})
	return mcpruntime.WithProgressReporter(ctx, sessionProgressReporter{session: session})
}

type sessionElicitor struct {
	session *mcp.ServerSession
}

type sessionSampler struct {
	session *mcp.ServerSession
}

type sessionRootLister struct {
	session *mcp.ServerSession
}

type sessionProgressReporter struct {
	session *mcp.ServerSession
}

func (e sessionElicitor) Elicit(ctx context.Context, req mcpruntime.ElicitRequest) (*mcpruntime.ElicitResult, error) {
	if e.session == nil {
		return nil, mcpruntime.ErrElicitorUnavailable
	}
	result, err := e.session.Elicit(ctx, &mcp.ElicitParams{
		ElicitationID:   req.ElicitationID,
		Message:         req.Message,
		Mode:            req.Mode,
		RequestedSchema: req.RequestedSchema,
		URL:             req.URL,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return &mcpruntime.ElicitResult{}, nil
	}
	return &mcpruntime.ElicitResult{
		Action:  result.Action,
		Content: result.Content,
	}, nil
}

func (s sessionSampler) Sample(ctx context.Context, req mcpruntime.SampleRequest) (*mcpruntime.SampleResult, error) {
	if s.session == nil {
		return nil, mcpruntime.ErrSamplerUnavailable
	}
	messages := make([]*mcp.SamplingMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		messages = append(messages, &mcp.SamplingMessage{
			Role:    mcp.Role(message.Role),
			Content: &mcp.TextContent{Text: message.Text},
		})
	}
	result, err := s.session.CreateMessage(ctx, &mcp.CreateMessageParams{
		Messages:      messages,
		SystemPrompt:  req.SystemPrompt,
		MaxTokens:     req.MaxTokens,
		StopSequences: req.StopSequences,
		Temperature:   req.Temperature,
		Metadata:      req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("sampling/createMessage returned a nil result")
	}
	content, ok := result.Content.(*mcp.TextContent)
	if !ok {
		return nil, fmt.Errorf("sampling/createMessage returned %T content; text sampling requires text content", result.Content)
	}
	return &mcpruntime.SampleResult{
		Role:       string(result.Role),
		Text:       content.Text,
		Model:      result.Model,
		StopReason: result.StopReason,
	}, nil
}

func (l sessionRootLister) ListRoots(ctx context.Context) ([]mcpruntime.Root, error) {
	if l.session == nil {
		return nil, mcpruntime.ErrRootListerUnavailable
	}
	result, err := l.session.ListRoots(ctx, nil)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return []mcpruntime.Root{}, nil
	}
	roots := make([]mcpruntime.Root, 0, len(result.Roots))
	for _, root := range result.Roots {
		if root == nil {
			continue
		}
		roots = append(roots, mcpruntime.Root{URI: root.URI, Name: root.Name})
	}
	return roots, nil
}

func (r sessionProgressReporter) ReportProgress(ctx context.Context, token any, update mcpruntime.ProgressUpdate) error {
	if r.session == nil {
		return mcpruntime.ErrProgressReporterUnavailable
	}
	return r.session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: token,
		Progress:      update.Progress,
		Total:         update.Total,
		Message:       update.Message,
	})
}
