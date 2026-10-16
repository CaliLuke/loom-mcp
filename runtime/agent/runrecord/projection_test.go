package runrecord_test

import (
	"encoding/json/jsontext"
	"errors"
	"slices"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectionPublicContentAndRawIsolation(t *testing.T) {
	message := &model.Message{Role: model.ConversationRoleAssistant, Meta: map[string]any{"secret": "private-meta"}, Parts: []model.Part{model.TextPart{Text: "public-answer"}, model.ThinkingPart{Text: "private-reason"}, model.ToolUsePart{ID: "tool1", Name: "lookup", Input: map[string]any{"query": "public-query"}}, model.ToolResultPart{ToolUseID: "tool1", Content: map[string]any{"result": "public-result"}, Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("private-image")}}}}}
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Transcript: []*model.Message{message}, Calls: []runrecord.Call{{Request: &model.Request{Messages: []*model.Message{message}}, Record: runstore.CallRecord{Started: []byte("private-opaque")}}}, ServerOnly: []runrecord.ContentPart{{Content: []byte(`"private-server"`)}}, Events: []*runlog.Event{{Type: hooks.ThinkingBlock, Payload: rawjson.Message(`{"text":"private-event"}`)}}}}, Content: map[runstore.Hash][]byte{"secret": []byte("private-content")}}
	response, err := callrecord.Encode(&model.Response{Content: []model.Message{*message}, ToolCalls: []model.ToolCall{{ID: "tool2", Name: "lookup", Payload: rawjson.Message(`{"query":"public-response-query"}`)}}})
	require.NoError(t, err)
	responseHash := runstore.Hash("response")
	record.Content[responseHash] = response
	record.Runs[0].Calls[0].Completion = &callrecord.Completion{Response: &responseHash}
	record.Runs[0].Results = []runrecord.ContentPart{{Content: []byte(`{"large":9007199254740993,"result":"public-stored-result"}`)}}
	before, err := record.Export().Marshal()
	require.NoError(t, err)
	projection, err := record.Project(publicPolicy())
	require.NoError(t, err)
	data, err := projection.Marshal()
	require.NoError(t, err)
	assert.Contains(t, string(data), "public-answer")
	assert.Contains(t, string(data), "public-query")
	assert.Contains(t, string(data), "public-result")
	assert.Contains(t, string(data), "public-response-query")
	assert.Contains(t, string(data), "public-stored-result")
	assert.Contains(t, string(data), "9007199254740993")
	assert.NotContains(t, string(data), "private-")
	assert.NotEmpty(t, projection.Omissions)
	assert.False(t, projection.Incomplete)
	decoded, err := runrecord.FromProjection(data)
	require.NoError(t, err)
	assert.Equal(t, projection.Source, decoded.Source)
	_, err = runrecord.FromExport(data)
	require.Error(t, err)
	after, err := record.Export().Marshal()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestProjectionOutcomesAndChildren(t *testing.T) {
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Calls: []runrecord.Call{{Class: runrecord.Adopted, Completion: &callrecord.Completion{Error: &callrecord.CallError{Kind: "canceled", Message: "private-error"}}}, {Class: runrecord.Incomplete}, {Class: runrecord.Abandoned}}, Events: []*runlog.Event{{Type: hooks.ChildRunLinked, Payload: rawjson.Message(`{"ToolCallID":"tool1","ToolName":"child","ChildRunID":"child","ChildAgentID":"assistant"}`)}, {Type: hooks.RunCompleted, Payload: rawjson.Message(`{"status":"canceled","phase":"canceled","error":"private-error"}`)}}}, {RunID: "child", Events: []*runlog.Event{{Type: hooks.ChildRunLinked, Payload: rawjson.Message(`{"ChildRunID":"grandchild"}`)}}}, {RunID: "grandchild"}}}
	record.Runs[0].Events = []*runlog.Event{
		projectionEvent(t, hooks.NewChildRunLinkedEvent("root", "parent-agent", "session", "child", "tool1", "child", "assistant")),
		projectionEvent(t, hooks.NewRunCompletedEvent("root", "parent-agent", "session", "canceled", "canceled", errors.New("private-error"))),
	}
	record.Runs[1].Events = []*runlog.Event{projectionEvent(t, hooks.NewChildRunLinkedEvent("child", "assistant", "session", "nested", "tool2", "grandchild", "grandchild-agent"))}
	projection, err := record.Project(publicPolicy())
	require.NoError(t, err)
	require.Len(t, projection.Runs, 3)
	require.Len(t, projection.Runs[0].Events, 2)
	assert.Equal(t, "child", projection.Runs[0].Events[0].ChildRunID)
	assert.Equal(t, "canceled", projection.Runs[0].Events[1].Status)
	assert.Equal(t, "grandchild", projection.Runs[1].Events[0].ChildRunID)
	require.Len(t, projection.Runs[0].Calls, 3)
	assert.Equal(t, "canceled", projection.Runs[0].Calls[0].ErrorKind)
	assert.Equal(t, runrecord.Incomplete, projection.Runs[0].Calls[1].Class)
	assert.Equal(t, runrecord.Abandoned, projection.Runs[0].Calls[2].Class)
}

func TestProjectionFailureIsExplicit(t *testing.T) {
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Transcript: []*model.Message{{Parts: []model.Part{model.TextPart{Text: "hello"}}}}}}}
	tests := []struct {
		name string
		fn   func(runrecord.ProjectionContent) (jsontext.Value, bool, error)
	}{
		{"policy error", func(runrecord.ProjectionContent) (jsontext.Value, bool, error) {
			return nil, false, errors.New("private-policy-error")
		}},
		{"invalid output", func(runrecord.ProjectionContent) (jsontext.Value, bool, error) {
			return jsontext.Value(`{`), true, nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projection, err := record.Project(runrecord.ProjectionPolicy{Name: "v1", Content: tt.fn})
			require.Error(t, err)
			require.NotNil(t, projection)
			assert.True(t, projection.Incomplete)
			assert.NotEmpty(t, projection.Failures)
			data, err := projection.Marshal()
			require.NoError(t, err)
			assert.NotContains(t, string(data), "private-policy-error")
			projection.Incomplete = false
			_, err = projection.Marshal()
			require.Error(t, err)
		})
	}
}

func TestProjectionPolicyOwnsTransformedValues(t *testing.T) {
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Transcript: []*model.Message{{Parts: []model.Part{model.TextPart{Text: "original"}, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("attachment")}}}}}}}
	output := jsontext.Value(`"replacement"`)
	projection, err := record.Project(runrecord.ProjectionPolicy{Name: "app/v2", Content: func(c runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return output, true, nil
	}})
	require.NoError(t, err)
	output[1] = 'X'
	data, err := projection.Marshal()
	require.NoError(t, err)
	assert.Contains(t, string(data), "replacement")
	assert.NotContains(t, string(data), "Xeplacement")
	assert.Equal(t, "original", record.Runs[0].Transcript[0].Parts[0].(model.TextPart).Text)
	_, err = record.Project(runrecord.ProjectionPolicy{})
	require.Error(t, err)
	_, err = runrecord.FromProjection([]byte(`{"kind":"wrong","version":1}`))
	require.Error(t, err)
}

func publicPolicy() runrecord.ProjectionPolicy {
	return runrecord.ProjectionPolicy{Name: "test/public/v1", Content: func(c runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return c.Value, !slices.Contains([]string{"reasoning", "metadata", "server_only", "attachment", "diagnostic", "event_payload"}, c.Kind), nil
	}}
}

func TestProjectionResponseFailures(t *testing.T) {
	tests := []struct {
		name    string
		content map[runstore.Hash][]byte
		code    string
	}{
		{"missing", nil, "missing_response"},
		{"malformed", map[runstore.Hash][]byte{"response": []byte(`{`)}, "invalid_response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := runstore.Hash("response")
			record := &runrecord.RunRecord{RootRunID: "root", Content: tt.content, Runs: []runrecord.Run{{RunID: "root", Calls: []runrecord.Call{{Completion: &callrecord.Completion{Response: &hash}}}}}}
			projection, err := record.Project(publicPolicy())
			require.Error(t, err)
			assert.True(t, projection.Incomplete)
			require.NotEmpty(t, projection.Failures)
			assert.Equal(t, tt.code, projection.Failures[0].Code)
		})
	}
}

func TestProjectionApprovedAttachmentsAndMetadata(t *testing.T) {
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Transcript: []*model.Message{{Role: model.ConversationRoleUser, Meta: map[string]any{"public": "metadata"}, Parts: []model.Part{model.DocumentPart{Name: "doc", Format: model.DocumentFormat("txt"), Text: "public-document"}, model.ToolResultPart{ToolUseID: "t1", Content: "result", Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("image")}}}}}}, ServerOnly: []runrecord.ContentPart{{Content: []byte(`{"public":"server"}`)}}}}}
	projection, err := record.Project(runrecord.ProjectionPolicy{Name: "app/allow/v1", Content: func(c runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return c.Value, true, nil
	}})
	require.NoError(t, err)
	require.Len(t, projection.Runs[0].Transcript[0].Parts, 2)
	assert.Equal(t, "attachment", projection.Runs[0].Transcript[0].Parts[0].Kind)
	require.Len(t, projection.Runs[0].Transcript[0].Parts[1].Attachments, 1)
	assert.JSONEq(t, `{"public":"metadata"}`, string(projection.Runs[0].Transcript[0].Metadata))
	require.Len(t, projection.Runs[0].Content, 1)
	assert.JSONEq(t, `{"public":"server"}`, string(projection.Runs[0].Content[0].Value))
}

func TestProjectionPointerParts(t *testing.T) {
	text := model.TextPart{Text: "answer"}
	reasoning := model.ThinkingPart{Text: "thinking"}
	tool := model.ToolUsePart{ID: "t1", Name: "lookup", Input: map[string]any{"query": "value"}}
	result := model.ToolResultPart{ToolUseID: "t1", Content: "result"}
	image := model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("image")}
	document := model.DocumentPart{Name: "document", Format: model.DocumentFormat("txt"), Text: "document"}
	citations := model.CitationsPart{Text: "cited"}
	cache := model.CacheCheckpointPart{}
	policy := runrecord.ProjectionPolicy{Name: "app/all/v1", Content: func(c runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return c.Value, true, nil
	}}
	record := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root", Transcript: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{text, reasoning, tool, result, image, document, citations, cache}}}}}}
	values, err := record.Project(policy)
	require.NoError(t, err)
	record.Runs[0].Transcript[0].Parts = []model.Part{&text, &reasoning, &tool, &result, &image, &document, &citations, &cache}
	pointers, err := record.Project(policy)
	require.NoError(t, err)
	assert.Equal(t, values.Runs, pointers.Runs)
	assert.Equal(t, values.Omissions, pointers.Omissions)
}

func projectionEvent(t *testing.T, event hooks.Event) *runlog.Event {
	t.Helper()
	input, err := hooks.EncodeToHookInput(event, "turn")
	require.NoError(t, err)
	return &runlog.Event{ID: "event", Type: input.Type, AgentID: input.AgentID, RunID: input.RunID, SessionID: input.SessionID, TurnID: input.TurnID, Payload: input.Payload}
}
