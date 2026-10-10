package runtime

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type indexOracle struct {
	t       *testing.T
	rt      *Runtime
	wf      *testWorkflowContext
	input   *RunInput
	base    *planner.PlanInput
	store   *inmem.Store
	entries []index.MessageEntry
}

func newIndexOracle(t *testing.T, seed []*model.Message) *indexOracle {
	t.Helper()
	store := inmem.New()
	rt := New(WithLogger(telemetry.NoopLogger{}))
	rt.RunStore = store
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt}
	wf.ensureSignals()
	input := &RunInput{AgentID: "test.agent", RunID: "root", RootRunID: "root", Messages: seed}
	o := &indexOracle{t: t, rt: rt, wf: wf, input: input, base: &planner.PlanInput{}, store: store}
	for _, message := range seed {
		o.add(index.MessageEntry{Kind: index.Message, Hash: o.putJSON(message)})
	}
	o.check("workflow.go base init")
	return o
}

func (o *indexOracle) put(data []byte) runstore.Hash {
	o.t.Helper()
	hash, err := o.store.Put(o.t.Context(), "root", data)
	require.NoError(o.t, err)
	return hash
}

func (o *indexOracle) putJSON(value any) runstore.Hash {
	o.t.Helper()
	data, err := json.Marshal(value)
	require.NoError(o.t, err)
	return o.put(data)
}

func (o *indexOracle) text(value string) index.TextParameter {
	if len(value) > index.MaxInlineTextBytes {
		return index.TextParameter{Hash: o.put([]byte(value))}
	}
	return index.TextParameter{Text: value}
}

func (o *indexOracle) add(entry index.MessageEntry) {
	entry.ID = index.MessageID(len(o.entries) + 1)
	o.entries = append(o.entries, entry)
}

func (o *indexOracle) structural(kind index.StructuralKind, ids []string, texts ...index.TextParameter) {
	o.add(index.MessageEntry{Kind: index.Structural, ToolUseIDs: ids, Structural: &index.StructuralEntry{
		Kind: kind, ProtocolVersion: index.TemplateProtocolVersion, Text: texts,
	}})
}

func (o *indexOracle) assistant(messages []*model.Message, calls []planner.ToolRequest) {
	entry := index.MessageEntry{Kind: index.Assistant, Role: model.ConversationRoleAssistant}
	if len(messages) > 0 {
		entry.Hash = o.putJSON(messages)
	}
	for _, call := range calls {
		entry.Calls = append(entry.Calls, o.putJSON(call))
		entry.ToolUseIDs = append(entry.ToolUseIDs, call.ToolCallID)
	}
	o.add(entry)
}

// results indexes only the source data, independently of the live message
// mutation. Caller-specified order is intentional, including omitted results.
func (o *indexOracle) results(results ...*planner.ToolResult) {
	batch := index.MessageID(len(o.entries) + 1)
	for _, result := range results {
		entry := index.MessageEntry{Kind: index.ToolResult, Batch: batch, ToolUseIDs: []string{result.ToolCallID}, Result: &index.ResultProjection{Bounds: result.Bounds}}
		if result.Result != nil {
			data, err := o.rt.marshalToolValue(o.t.Context(), result.Name, result.Result, result.Bounds)
			require.NoError(o.t, err)
			entry.Hash = o.put(data)
		}
		entry.Result.Preview = o.text(o.rt.formatResultPreview(o.t.Context(), result.Name, nil, result.Result, result.Bounds))
		if result.Error != nil {
			text := o.text(result.Error.Error())
			entry.Result.Error = &text
		}
		entry.Record = []runstore.Hash{"server-only:must-not-load"}
		o.add(entry)
	}
}

func (o *indexOracle) check(site string) {
	o.t.Helper()
	before, err := json.Marshal(o.entries, json.Deterministic(true))
	require.NoError(o.t, err)
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return o.store.Get(ctx, "root", hash)
	}
	actual, err := index.Render(o.t.Context(), o.entries, source)
	require.NoError(o.t, err, site)
	fixtures := map[string]jsontext.Value{}
	data, err := os.ReadFile("testdata/k7_messages.json")
	require.NoError(o.t, err)
	require.NoError(o.t, json.Unmarshal(data, &fixtures))
	key := fmt.Sprintf("%s/%d/%s", o.t.Name(), len(o.entries), site)
	want, ok := fixtures[key]
	require.True(o.t, ok, "missing frozen pre-K7 expectation: %s", key)
	assert.Equal(o.t, []byte(want), canonicalIndexMessages(o.t, actual), site)
	o.base.Messages = actual
	after, err := json.Marshal(o.entries, json.Deterministic(true))
	require.NoError(o.t, err)
	assert.Equal(o.t, before, after, "renderer must not mutate entries")
}

func canonicalIndexMessages(t *testing.T, messages []*model.Message) []byte {
	t.Helper()
	data, err := json.Marshal(messages)
	require.NoError(t, err)
	value := jsontext.Value(data)
	require.NoError(t, value.Canonicalize())
	return value
}

func TestIndexOracleAssistantShapes(t *testing.T) {
	call := planner.ToolRequest{Name: "test.lookup", ToolCallID: "call-1", Payload: []byte(`{"q":"x"}`)}
	for _, tc := range []struct {
		name       string
		transcript []*model.Message
		calls      []planner.ToolRequest
	}{
		{name: "empty"},
		{name: "calls only", calls: []planner.ToolRequest{call}},
		{name: "nil transcript element", transcript: []*model.Message{nil}},
		{name: "text only", transcript: []*model.Message{newTextAgentMessage(model.ConversationRoleAssistant, "answer")}},
		{name: "no assistant", transcript: []*model.Message{newTextAgentMessage(model.ConversationRoleSystem, "context")}, calls: []planner.ToolRequest{call}},
		{name: "last assistant", transcript: []*model.Message{
			nil,
			{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "reason", Signature: "signed"}}},
			{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "doing it"}}, Meta: map[string]any{"trace": "a"}},
			newTextAgentMessage(model.ConversationRoleSystem, "after"),
		}, calls: []planner.ToolRequest{call}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newIndexOracle(t, nil)
			o.assistant(tc.transcript, tc.calls)
			o.check("recordAssistantTurn")
		})
	}
}
