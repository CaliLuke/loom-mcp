package runtime

import (
	jsonopts "encoding/json"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
)

// prepareRunStartedHook stores oversized effective inputs before publishing a
// reference. The planner and run interceptors retain the original input.
func prepareRunStartedHook(wf engine.WorkflowContext, input *RunInput, runCtx run.Context, turnID string) (*hooks.RunStartedEvent, error) {
	event := hooks.NewRunStartedEvent(input.RunID, input.AgentID, runCtx, *input)
	encoded, err := hooks.EncodeToHookInput(event, turnID)
	if err != nil {
		return nil, err
	}
	if len(encoded.Payload) <= maxHookPayloadBytes {
		return event, nil
	}
	data, err := json.Marshal(input, json.FormatNilMapAsNull(true), json.FormatNilSliceAsNull(true), jsonopts.FormatDurationAsNano(true))
	if err != nil {
		return nil, fmt.Errorf("encode run started input: %w", err)
	}
	stored, err := wf.ExecuteRecordContentActivity(wf.Context(), engine.RecordContentActivityCall{
		Name:  RecordContentActivityName(input.AgentID),
		Input: &api.RecordContentInput{RootRunID: input.RootRunID, Items: [][]byte{data}},
	})
	if err != nil {
		return nil, fmt.Errorf("store run started input: %w", err)
	}
	event.Input = nil
	event.InputRef = stored.Refs[0]
	return event, nil
}
