package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// storeSignalContent resolves scope from the execution, never from caller input
// or a process-local run cache. Client processes must share workers' run store.
func (r *Runtime) storeSignalContent(ctx context.Context, runID string, items [][]byte) ([]runstore.Hash, error) {
	root, err := r.Engine.QueryRootRunID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("query root run id: %w", err)
	}
	out, err := r.RecordContentActivity(ctx, &api.RecordContentInput{RootRunID: root, Items: items})
	if err != nil {
		return nil, err
	}
	return out.Refs, nil
}

func (r *Runtime) storeResumeRequest(ctx context.Context, req *api.ResumeRequest) (*api.ResumeRequest, error) {
	items, err := encodeContentMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	refs, err := r.storeSignalContent(ctx, req.RunID, items)
	if err != nil {
		return nil, err
	}
	out := *req
	out.MessageSizes = make([]contextmeasure.MessageSize, len(req.Messages))
	for i, message := range req.Messages {
		size, err := contextmeasure.MeasureMessages([]*model.Message{message})
		if err != nil {
			return nil, err
		}
		out.MessageSizes[i] = size
	}
	out.MessageRefs = refs
	out.Messages = nil
	return &out, nil
}

func (r *Runtime) storeProvidedResults(ctx context.Context, input *api.ToolResultsSet) (*api.ToolResultsSet, error) {
	out := *input
	out.Results = make([]*api.ProvidedToolResult, len(input.Results))
	var items [][]byte
	for i, result := range input.Results {
		if result == nil {
			continue
		}
		copyResult := *result
		copyResult.Refs = tools.ResultRefs{}
		out.Results[i] = &copyResult
		data := []byte(result.Result)
		if len(data) == 0 {
			data = []byte("null")
		}
		items = append(items, data)
	}
	refs, err := r.storeSignalContent(ctx, input.RunID, items)
	if err != nil {
		return nil, err
	}
	for _, result := range out.Results {
		if result != nil {
			result.Refs.Result = refs[0]
			result.Result = nil
			refs = refs[1:]
		}
	}
	return &out, nil
}
