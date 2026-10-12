package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

// RecallActivityName identifies the lookup activity registered on an agent's queue.
func RecallActivityName(id agent.Ident) string {
	return string(id) + ".recall"
}

// RecallActivity resolves eligible evidence and stores the rendered result before
// returning only its address and bounded preview. Session identity comes from the store.
func (r *Runtime) RecallActivity(ctx context.Context, input *api.RecallActivityInput) (*api.RecallActivityOutput, error) {
	if input == nil || input.RunID == "" || input.Scope == "" {
		return nil, fmt.Errorf("recall activity: run and scope are required")
	}
	parsed, parseErr := recall.ParseInput(input.Call.Payload)
	response := recall.Response{Code: recall.CodeInvalidInput}
	if parseErr == nil && input.Call.Error == nil {
		handler := recall.Handler{Store: r.RunStore, Sessions: r.SessionStore, Authorize: r.recallAuthorizer}
		chain, err := r.continuationChain(ctx, input.Scope, input.RunID)
		if err != nil {
			return nil, err
		}
		if chain != nil {
			handler.Chain = chain
		}
		response, err = handler.Handle(ctx, recall.Request{RunID: input.RunID, Scope: input.Scope, Head: input.Head, View: input.ViewRef, PageBytes: input.PageBytes, ScanBytes: input.ScanBytes}, parsed)
		if err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(response, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	hash, err := r.RunStore.Put(ctx, input.Scope, data)
	if err != nil {
		return nil, err
	}
	return &api.RecallActivityOutput{Code: response.Code, Bytes: len(data), Result: &api.ToolOutput{ResultBytes: len(data), Refs: tools.ResultRefs{Result: hash}, Preview: "context lookup result"}}, nil
}
