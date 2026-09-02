package runtime

import (
	"context"
	"errors"
	"fmt"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type modelInterceptedClient struct {
	inner        model.Client
	interceptors []Interceptor
	agentID      agent.Ident
	runID        string
	sessionID    string
	turnID       string
	modelID      string
	recovery     *modelRecoveryRecorder
	configureReq func(*model.Request)
}

func newModelInterceptedClient(inner model.Client, interceptors []Interceptor, agentID agent.Ident, runID, sessionID, turnID, modelID string, recovery *modelRecoveryRecorder, configureReq func(*model.Request)) model.Client {
	if inner == nil {
		return inner
	}
	if !hasModelInterceptors(interceptors) {
		return newRecoveryCapturingClient(inner, recovery)
	}
	return &modelInterceptedClient{
		inner:        inner,
		interceptors: append([]Interceptor(nil), interceptors...),
		agentID:      agentID,
		runID:        runID,
		sessionID:    sessionID,
		turnID:       turnID,
		modelID:      modelID,
		recovery:     recovery,
		configureReq: configureReq,
	}
}

func (c *modelInterceptedClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	currentReq, resp, err, short := c.beforeModel(ctx, req)
	c.configureRequest(currentReq)
	effectiveReq, snapshotErr := separateEffectiveModelRequest(req, currentReq)
	if snapshotErr != nil {
		return nil, snapshotErr
	}
	currentReq = effectiveReq
	contract, contractErr := model.NewRequestContract(currentReq)
	if contractErr != nil {
		return nil, contractErr
	}
	if short {
		return c.afterModel(ctx, currentReq, contract, resp, err)
	}
	resp, err = c.inner.Complete(ctx, currentReq)
	return c.afterModel(ctx, currentReq, contract, resp, err)
}

func (c *modelInterceptedClient) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	currentReq, resp, err, short := c.beforeModel(ctx, req)
	c.configureRequest(currentReq)
	effectiveReq, snapshotErr := separateEffectiveModelRequest(req, currentReq)
	if snapshotErr != nil {
		return nil, snapshotErr
	}
	currentReq = effectiveReq
	if short {
		if err == nil && resp != nil {
			err = errors.New("model response short-circuit is unsupported for streaming")
		}
		return nil, err
	}
	recoveryRequest, recoveryRequestErr := cloneRecoveryRequest(currentReq)
	st, err := c.inner.Stream(ctx, currentReq)
	err = c.afterModelStream(ctx, currentReq, err)
	if err != nil && st != nil {
		err = st.Finalize(err)
	}
	if err != nil {
		c.recovery.recordSnapshot(recoveryRequest, recoveryRequestErr, err, false)
		return nil, err
	}
	if err == nil && st == nil {
		return nil, fmt.Errorf("model stream contract violation: nil streamer with nil error")
	}
	if c.recovery != nil {
		return &recoveryCapturingStream{
			inner:      st,
			recorder:   c.recovery,
			request:    recoveryRequest,
			requestErr: recoveryRequestErr,
		}, nil
	}
	return st, nil
}

func (c *modelInterceptedClient) configureRequest(req *model.Request) {
	if c.configureReq != nil {
		c.configureReq(req)
	}
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

func (c *modelInterceptedClient) afterModel(ctx context.Context, req *model.Request, contract *model.RequestContract, resp *model.Response, callErr error) (*model.Response, error) {
	recoveryRequest, recoveryRequestErr := cloneRecoveryRequest(req)
	currentResp, currentErr := c.applyAfterModel(ctx, req, resp, callErr)
	if currentErr != nil {
		c.recovery.recordSnapshot(recoveryRequest, recoveryRequestErr, currentErr, false)
		return currentResp, currentErr
	}
	if currentErr == nil && currentResp == nil {
		return nil, fmt.Errorf("model complete contract violation: nil response with nil error")
	}
	validated, err := contract.ValidateResponse(currentResp)
	c.recovery.recordSnapshot(recoveryRequest, recoveryRequestErr, err, false)
	return validated, err
}

func (c *modelInterceptedClient) afterModelStream(ctx context.Context, req *model.Request, callErr error) error {
	resp, err := c.applyAfterModel(ctx, req, nil, callErr)
	if resp != nil {
		return errors.Join(callErr, err, errors.New("model response replacement is unsupported for streaming"))
	}
	return err
}

func (c *modelInterceptedClient) applyAfterModel(ctx context.Context, req *model.Request, resp *model.Response, callErr error) (*model.Response, error) {
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
			currentErr = decision.Err
			continue
		}
		if decision.Err != nil {
			currentErr = decision.Err
		}
	}
	return currentResp, currentErr
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

// separateEffectiveModelRequest exposes an immutable effective snapshot to
// outer decorators and returns a request that after-model interceptors may
// mutate without changing that snapshot.
func separateEffectiveModelRequest(original, effective *model.Request) (*model.Request, error) {
	if original == nil || effective == nil {
		return effective, nil
	}
	outerSnapshot, err := model.CloneRequest(effective)
	if err != nil {
		return nil, fmt.Errorf("snapshot effective model request: %w", err)
	}
	*original = *outerSnapshot
	if original != effective {
		return effective, nil
	}
	callRequest, err := model.CloneRequest(effective)
	if err != nil {
		return nil, fmt.Errorf("separate effective model request: %w", err)
	}
	return callRequest, nil
}
