package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryRejectsInvalidCutBeforeModelDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*SummarizeContextInput)
		want   string
	}{
		{"past end", func(in *SummarizeContextInput) {
			in.To = 99
		}, "cut exceeds index"},
		{"split handshake", func(in *SummarizeContextInput) {
			in.To = 3
		}, ""},
		{"invalid starting tail", func(in *SummarizeContextInput) {
			in.From = 3
			in.ViewRef.TailFrom = 3
		}, ""},
		{"pending gap", func(in *SummarizeContextInput) {
			in.ViewRef.Pending = []index.MessageEntry{{ID: 8, Kind: index.Message}}
		}, "pending index gap"},
		{"pending chain", func(in *SummarizeContextInput) {
			in.ViewRef.Pending = []index.MessageEntry{{ID: 6, Kind: index.Message, Chain: "wrong"}}
		}, "pending chain mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			tc.mutate(in)
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
			require.Error(t, err)
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
			}
			assert.Nil(t, out)
			assert.Empty(t, client.requests)
		})
	}
}

func TestSummaryRejectsForeignOrMalformedPredecessor(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"malformed", `{`, ""},
		{"unknown member", `{"unknown":true}`, "unknown"},
		{"wrong cut", `{"through":1}`, "checkpoint does not match From"},
		{"foreign unrecorded summary", `{"through":0,"summary":"missing","summary_run":"other","summary_pos":0}`, "no recorded earlier chain position"},
		{"future summary", `{"through":0,"summary":"missing","summary_run":"other","summary_pos":4}`, "no recorded earlier chain position"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, in, client, _ := summaryFixture(t)
			body := []byte(tc.body)
			hash, err := rt.RunStore.Put(t.Context(), in.Scope, body)
			require.NoError(t, err)
			in.ViewRef.Checkpoint = &hash
			in.ViewRef.ChainPos = 2
			out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(t.Context(), 1), in)
			require.Error(t, err)
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
			}
			assert.Nil(t, out)
			assert.Empty(t, client.requests)
		})
	}
}
