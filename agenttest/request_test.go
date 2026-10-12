package agenttest_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestFixtureIntegrity(t *testing.T) {
	req := &model.Request{Model: "provider", PromptRefs: []prompt.PromptRef{{ID: "system", Version: "9007199254740993"}}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "original"}}, Meta: map[string]any{"large": jsontext.Value(`9007199254740993`)}}}, Tools: []*model.ToolDefinition{{Name: "echo.echo", InputSchema: map[string]any{"type": "object", "maximum": jsontext.Value(`9007199254740993`)}}}}
	r, err := agenttest.NewRequest("registered", req)
	require.NoError(t, err)
	data, err := agenttest.MarshalRequest(r)
	require.NoError(t, err)
	decoded, err := agenttest.UnmarshalRequest(data)
	require.NoError(t, err)
	again, err := agenttest.MarshalRequest(decoded)
	require.NoError(t, err)
	assert.Equal(t, data, again)
	assert.Equal(t, "registered", decoded.ModelID())
	assert.Nil(t, decoded.Source())
	var envelope struct {
		Digest string `json:"digest"`
	}
	require.NoError(t, json.Unmarshal(data, &envelope))
	digest, err := callrecord.RequestDigest(req)
	require.NoError(t, err)
	assert.Equal(t, string(digest), envelope.Digest)
	for _, tc := range []struct {
		name     string
		data     []byte
		mismatch bool
	}{
		{"tampered", bytes.Replace(data, []byte("original"), []byte("tampered"), 1), true},
		{"unknown", append([]byte(`{"unknown":true,`), data[1:]...), false},
		{"version", bytes.Replace(data, []byte(`"version":1`), []byte(`"version":2`), 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := agenttest.UnmarshalRequest(tc.data)
			require.Error(t, err)
			if tc.mismatch {
				assert.ErrorIs(t, err, callrecord.ErrRequestMismatch)
			}
		})
	}
	// Both decoded requests and provenance returned to callers are owned copies.
	owned, err := decoded.Request()
	require.NoError(t, err)
	owned.Messages[0].Meta["large"] = 0
	unchanged, err := agenttest.MarshalRequest(decoded)
	require.NoError(t, err)
	assert.Equal(t, data, unchanged)
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, agenttest.SaveRequest(path, r))
	loaded := agenttest.LoadRequest(t, path)
	saved, err := agenttest.MarshalRequest(loaded)
	require.NoError(t, err)
	assert.Equal(t, data, saved)
}

func TestRequestFixtureRejectsUnknownRequestMembers(t *testing.T) {
	r, err := agenttest.NewRequest("registered", &model.Request{
		Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hi"}}}},
		Tools:    []*model.ToolDefinition{{Name: "echo.echo", InputSchema: map[string]any{"type": "object"}}},
	})
	require.NoError(t, err)
	data, err := agenttest.MarshalRequest(r)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, member string
	}{
		{"request", `"request":{`},
		{"message", `"Messages":[{`},
		{"part", `"Parts":[{`},
		{"tool", `"Tools":[{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, string(data), tc.member)
			mutated := bytes.Replace(data, []byte(tc.member), []byte(tc.member+`"unknown":true,`), 1)
			_, err := agenttest.UnmarshalRequest(mutated)
			require.ErrorContains(t, err, "unknown or non-canonical members")
			assert.NotErrorIs(t, err, callrecord.ErrRequestMismatch)
		})
	}
}

func TestRequestFixtureAcceptsJSONFormatting(t *testing.T) {
	r, err := agenttest.NewRequest("registered", &model.Request{Model: "provider"})
	require.NoError(t, err)
	data, err := agenttest.MarshalRequest(r)
	require.NoError(t, err)
	var envelope map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(data, &envelope))
	request := bytes.Replace(envelope["request"], []byte(`"Model":"provider",`), nil, 1)
	request = append([]byte(`{"Model":"provider",`), request[1:]...)
	envelope["request"] = request
	formatted, err := json.Marshal(envelope, jsontext.WithIndent("  "))
	require.NoError(t, err)
	decoded, err := agenttest.UnmarshalRequest(formatted)
	require.NoError(t, err)
	again, err := agenttest.MarshalRequest(decoded)
	require.NoError(t, err)
	assert.Equal(t, data, again)
}

func TestRequestTransforms(t *testing.T) {
	original := &model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "first"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "old"}}, Meta: map[string]any{"drop": true}},
		{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "second"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "result"}}},
	}, Tools: []*model.ToolDefinition{{Name: "echo.echo", Description: "old"}, {Name: "tool_unavailable"}}}
	r, err := agenttest.NewRequest("model", original)
	require.NoError(t, err)
	before, err := agenttest.MarshalRequest(r)
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		transform func(agenttest.Request) agenttest.Request
		check     func(*model.Request)
	}{
		{"user", func(r agenttest.Request) agenttest.Request {
			return r.WithUserMessage("new")
		}, func(req *model.Request) {
			assert.Equal(t, []model.Part{model.TextPart{Text: "new"}}, req.Messages[1].Parts)
			assert.Empty(t, req.Messages[1].Meta)
			assert.IsType(t, model.ToolResultPart{}, req.Messages[3].Parts[0])
		}},
		{"system", func(r agenttest.Request) agenttest.Request {
			return r.WithSystem("new system")
		}, func(req *model.Request) {
			require.Len(t, req.Messages, 3)
			assert.Equal(t, model.TextPart{Text: "new system"}, req.Messages[0].Parts[0])
		}},
		{"tools", func(r agenttest.Request) agenttest.Request {
			return r.WithToolSpecs(tools.ToolSpec{Name: "echo.echo", Description: "replacement"}, tools.ToolSpec{Name: "a.a"}, tools.ToolSpec{Name: "b.b"})
		}, func(req *model.Request) {
			require.Len(t, req.Tools, 4)
			assert.Equal(t, "replacement", req.Tools[0].Description)
			assert.Equal(t, "tool_unavailable", req.Tools[1].Name)
			assert.Equal(t, "a.a", req.Tools[2].Name)
			assert.Equal(t, "b.b", req.Tools[3].Name)
			assert.Equal(t, map[string]any{"type": "object"}, req.Tools[0].InputSchema)
		}},
		{"model", func(r agenttest.Request) agenttest.Request {
			return r.WithModel("new provider")
		}, func(req *model.Request) {
			assert.Equal(t, "new provider", req.Model)
		}},
		{"run", func(r agenttest.Request) agenttest.Request {
			return r.WithRunID("new run")
		}, func(req *model.Request) {
			assert.Equal(t, "new run", req.RunID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := tc.transform(r)
			req, err := changed.Request()
			require.NoError(t, err)
			tc.check(req)
			after, err := agenttest.MarshalRequest(r)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
	empty, err := agenttest.NewRequest("model", &model.Request{})
	require.NoError(t, err)
	inserted, err := empty.WithSystem("inserted").Request()
	require.NoError(t, err)
	require.Len(t, inserted.Messages, 1)
	assert.Equal(t, model.ConversationRoleSystem, inserted.Messages[0].Role)
	invalid := empty.WithUserMessage("missing")
	require.Error(t, invalid.Err())
	_, err = invalid.Request()
	require.Error(t, err)
	_, err = agenttest.MarshalRequest(invalid)
	require.Error(t, err)
	_, err = r.WithToolSpecs(tools.ToolSpec{Name: "bad", Payload: tools.TypeSpec{Schema: []byte(`{`)}}).Request()
	require.Error(t, err)
}

func TestSaveRequestConfinesSymlinks(t *testing.T) {
	r, err := agenttest.NewRequest("model", &model.Request{Messages: agenttest.User("private")})
	require.NoError(t, err)
	outside := filepath.Join(t.TempDir(), "outside.json")
	require.NoError(t, os.WriteFile(outside, []byte("unchanged"), 0o600))
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.Symlink(outside, path))
	require.Error(t, agenttest.SaveRequest(path, r))
	root, err := os.OpenRoot(filepath.Dir(outside))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, root.Close()) })
	data, err := root.ReadFile("outside.json")
	require.NoError(t, err)
	assert.Equal(t, "unchanged", string(data))
}
