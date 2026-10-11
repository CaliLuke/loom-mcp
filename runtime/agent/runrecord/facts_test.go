package runrecord_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestFactsReadAndExport(t *testing.T) {
	f := newRecordFixture()
	outcomes := []contextmeasure.RequestOutcome{contextmeasure.OutcomeOK, contextmeasure.OutcomeOverflow, contextmeasure.OutcomeError, ""}
	want := make([]*contextmeasure.RequestFacts, len(outcomes))
	for i, outcome := range outcomes {
		req := &model.Request{Model: "fake", Messages: []*model.Message{textMessage("request")}}
		parts, _, err := callrecord.BuildRequest(t.Context(), f.store, "root", req, index.ViewRef{}, nil)
		require.NoError(t, err)
		key := runstore.CallKey{RunID: "run", Seq: i + 1, Attempt: 2}
		started, err := callrecord.Encode(callrecord.Started{Key: key, Activity: callrecord.ActivityPlanner, Model: req.Model, Request: parts})
		require.NoError(t, err)
		record := runstore.CallRecord{Key: key, State: runstore.CallStarted, Started: started}
		require.NoError(t, f.store.PutCall(t.Context(), "root", record))
		if outcome == "" {
			continue
		}
		want[i] = &contextmeasure.RequestFacts{Call: key, Model: "canonical", ContextWindow: 8000, OutputReserve: 200, ViewBearing: true, InputTokens: 123, TextBytes: 369, FixedBytes: 3, NonText: 1500, CountLatency: 45 * time.Nanosecond, Outcome: outcome}
		if outcome == contextmeasure.OutcomeError {
			want[i].CountLatency = 0
		}
		completion := callrecord.Completion{Facts: want[i], Latency: 123 * time.Nanosecond}
		record.State = runstore.CallCompleted
		if outcome != contextmeasure.OutcomeOK {
			record.State = runstore.CallFailed
			completion.Error = &callrecord.CallError{Kind: string(outcome), Message: "failed"}
		}
		record.Completion, err = callrecord.Encode(completion)
		require.NoError(t, err)
		require.NoError(t, f.store.PutCall(t.Context(), "root", record))
	}
	record, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
	require.NoError(t, err)
	require.Len(t, record.Runs, 1)
	require.Len(t, record.Runs[0].Calls, len(outcomes))
	for i, call := range record.Runs[0].Calls {
		if want[i] == nil {
			assert.Nil(t, call.Completion)
			continue
		}
		require.NotNil(t, call.Completion)
		assert.Equal(t, want[i], call.Completion.Facts)
	}
	data, err := record.Export().Marshal()
	require.NoError(t, err)
	var exported struct {
		Runs []struct {
			Calls []struct {
				Completion struct {
					Facts jsontext.Value `json:"facts"`
				} `json:"completion"`
			} `json:"calls"`
		} `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(data, &exported))
	require.Len(t, exported.Runs, 1)
	require.Len(t, exported.Runs[0].Calls, len(outcomes))
	assert.JSONEq(t, `{"call":{"RunID":"run","Seq":1,"Attempt":2},"model":"canonical","context_window":8000,"output_reserve":200,"view_bearing":true,"input_tokens":123,"text_bytes":369,"fixed_bytes":3,"non_text":1500,"count_latency":45,"outcome":"ok"}`, string(exported.Runs[0].Calls[0].Completion.Facts))

	assert.Contains(t, string(exported.Runs[0].Calls[2].Completion.Facts), `"count_latency":0`)

	restored, err := runrecord.FromExport(data)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
	again, err := restored.Export().Marshal()
	require.NoError(t, err)
	assert.Equal(t, data, again)
	invalid := bytes.Replace(data, []byte(`"facts":{`), []byte(`"facts":{"unexpected":true,`), 1)
	require.NotEqual(t, data, invalid)
	_, err = runrecord.FromExport(invalid)
	require.ErrorContains(t, err, "unexpected")
}
