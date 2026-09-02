package gateway

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type (
	// RemoteClient is the validated consumer-side model gateway client.
	RemoteClient struct {
		client          model.Client
		constructionErr error
	}

	remoteProvider struct {
		doComplete func(ctx context.Context, req *model.Request) (*model.Response, error)
		doStream   func(ctx context.Context, req *model.Request) (model.Streamer, error)
	}
)

// NewRemoteClient constructs a validated model client around a raw remote
// provider. Validation therefore runs after provider and gateway middleware
// output crosses the transport.
func NewRemoteClient(
	complete func(ctx context.Context, req *model.Request) (*model.Response, error),
	stream func(ctx context.Context, req *model.Request) (model.Streamer, error),
) *RemoteClient {
	provider, err := NewRemoteProvider(complete, stream)
	if err != nil {
		return &RemoteClient{constructionErr: err}
	}
	client, err := model.NewClient(provider)
	return &RemoteClient{client: client, constructionErr: err}
}

// NewRemoteProvider constructs the raw transport provider used on the server
// side of a model gateway.
func NewRemoteProvider(
	complete func(ctx context.Context, req *model.Request) (*model.Response, error),
	stream func(ctx context.Context, req *model.Request) (model.Streamer, error),
) (model.Provider, error) {
	if complete == nil {
		return nil, errors.New("gateway: complete callback is required")
	}
	if stream == nil {
		return nil, errors.New("gateway: stream callback is required")
	}
	return &remoteProvider{doComplete: complete, doStream: stream}, nil
}

func (c *remoteProvider) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	return c.doComplete(ctx, req)
}

func (c *remoteProvider) Stream(ctx context.Context, req *model.Request) (model.Streamer, error) {
	return c.doStream(ctx, req)
}

// Complete invokes the remote provider through the local validation boundary.
func (c *RemoteClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	if c == nil {
		return nil, errors.New("gateway: remote client is required")
	}
	if c.constructionErr != nil {
		return nil, c.constructionErr
	}
	return c.client.Complete(ctx, req)
}

// Stream invokes the remote provider through the local validation boundary.
func (c *RemoteClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	if c == nil {
		return nil, errors.New("gateway: remote client is required")
	}
	if c.constructionErr != nil {
		return nil, c.constructionErr
	}
	return c.client.Stream(ctx, req)
}
