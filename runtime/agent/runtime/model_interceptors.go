package runtime

import (
	"context"
	"errors"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

type modelInterceptedClient struct {
	inner        model.Client
	interceptors []Interceptor
	agentID      agent.Ident
	runID        string
	sessionID    string
	turnID       string
	modelID      string
}

func newModelInterceptedClient(inner model.Client, interceptors []Interceptor, agentID agent.Ident, runID, sessionID, turnID, modelID string) model.Client {
	if inner == nil || !hasModelInterceptors(interceptors) {
		return inner
	}
	return &modelInterceptedClient{
		inner:        inner,
		interceptors: append([]Interceptor(nil), interceptors...),
		agentID:      agentID,
		runID:        runID,
		sessionID:    sessionID,
		turnID:       turnID,
		modelID:      modelID,
	}
}

func (c *modelInterceptedClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	currentReq, resp, err, short := c.beforeModel(ctx, req)
	if short {
		return c.afterModel(ctx, currentReq, resp, err)
	}
	resp, err = c.inner.Complete(ctx, currentReq)
	return c.afterModel(ctx, currentReq, resp, err)
}

func (c *modelInterceptedClient) Stream(ctx context.Context, req *model.Request) (model.Streamer, error) {
	currentReq, resp, err, short := c.beforeModel(ctx, req)
	if short {
		if err == nil && resp != nil {
			err = errors.New("model response short-circuit is unsupported for streaming")
		}
		return nil, err
	}
	st, err := c.inner.Stream(ctx, currentReq)
	err = c.afterModelStream(ctx, currentReq, err)
	return st, err
}

func (c *modelInterceptedClient) beforeModel(ctx context.Context, req *model.Request) (*model.Request, *model.Response, error, bool) {
	currentReq := req
	for _, interceptor := range c.interceptors {
		modelInterceptor, ok := interceptor.(ModelInterceptor)
		if !ok || modelInterceptor == nil {
			continue
		}
		decision, err := modelInterceptor.BeforeModel(ctx, c.beforeInput(currentReq))
		if err != nil {
			return currentReq, nil, err, true
		}
		if decision == nil {
			continue
		}
		if decision.Request != nil {
			currentReq = decision.Request
		}
		if decision.Response != nil || decision.Err != nil {
			return currentReq, decision.Response, decision.Err, true
		}
	}
	return currentReq, nil, nil, false
}

func (c *modelInterceptedClient) afterModel(ctx context.Context, req *model.Request, resp *model.Response, callErr error) (*model.Response, error) {
	currentResp := resp
	currentErr := callErr
	for _, interceptor := range c.interceptors {
		modelInterceptor, ok := interceptor.(ModelInterceptor)
		if !ok || modelInterceptor == nil {
			continue
		}
		decision, err := modelInterceptor.AfterModel(ctx, c.afterInput(req, currentResp, currentErr))
		if err != nil {
			return currentResp, err
		}
		if decision == nil {
			continue
		}
		if decision.Response != nil {
			currentResp = decision.Response
		}
		currentErr = decision.Err
	}
	return currentResp, currentErr
}

func (c *modelInterceptedClient) afterModelStream(ctx context.Context, req *model.Request, callErr error) error {
	_, err := c.afterModel(ctx, req, nil, callErr)
	return err
}

func (c *modelInterceptedClient) beforeInput(req *model.Request) *BeforeModelInput {
	return &BeforeModelInput{
		AgentID:   c.agentID,
		RunID:     c.runID,
		SessionID: c.sessionID,
		TurnID:    c.turnID,
		ModelID:   c.modelID,
		Request:   req,
	}
}

func (c *modelInterceptedClient) afterInput(req *model.Request, resp *model.Response, err error) *AfterModelInput {
	return &AfterModelInput{
		AgentID:   c.agentID,
		RunID:     c.runID,
		SessionID: c.sessionID,
		TurnID:    c.turnID,
		ModelID:   c.modelID,
		Request:   req,
		Response:  resp,
		Err:       err,
	}
}

func hasModelInterceptors(interceptors []Interceptor) bool {
	for _, interceptor := range interceptors {
		if _, ok := interceptor.(ModelInterceptor); ok {
			return true
		}
	}
	return false
}
