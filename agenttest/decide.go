package agenttest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Decision is the evidence from a single model invocation, without tool execution.
type Decision struct {
	ModelID  string
	Request  *model.Request
	Response *model.Response
	Calls    []model.ToolCall
	Latency  time.Duration
}

// DecideContext invokes a client directly without adding CallInfo or recording
// a run. Streaming requests are drained and finalized before returning evidence.
func DecideContext(ctx context.Context, client model.Client, r Request) (*Decision, error) {
	if client == nil {
		return nil, errors.New("agenttest: model client is nil; is the ID registered?")
	}
	req, err := r.Request()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	var response *model.Response
	if req.Stream {
		response, err = decideStream(ctx, client, req)
	} else {
		response, err = client.Complete(ctx, req)
		if err == nil {
			var contract *model.RequestContract
			contract, err = model.NewRequestContract(req)
			if err == nil {
				response, err = contract.ValidateResponse(response)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return &Decision{ModelID: r.modelID, Request: req, Response: response, Calls: response.ToolCalls, Latency: time.Since(start)}, nil
}

// Decide invokes one model using t's context and fails t on any error.
func Decide(t testing.TB, client model.Client, r Request) *Decision {
	t.Helper()
	decision, err := DecideContext(t.Context(), client, r)
	if err != nil {
		var validation *model.OutputValidationError
		if errors.As(err, &validation) {
			t.Fatal(fmt.Errorf("agenttest: Decide: %s: %w", validation.Kind(), err))
		} else {
			t.Fatal(err)
		}
	}
	return decision
}

func decideStream(ctx context.Context, client model.Client, req *model.Request) (*model.Response, error) {
	stream, err := client.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	//nolint:errorlint // Only literal EOF proves validated completion.
	if err == io.EOF {
		err = nil
	}
	if finalErr := stream.Finalize(err); finalErr != nil {
		return nil, errors.Join(err, finalErr)
	}
	if err != nil {
		return nil, err
	}
	return stream.Response(), nil
}
