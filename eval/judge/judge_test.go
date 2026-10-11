package judge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rawClient struct {
	unusedClient
	script *agenttest.Script
}

const validPayload = `{"b":{"label":"contradicted","rationale":"The sky is blue."},"a":{"label":"entailed","rationale":"The subject says blue."}}`

func TestJudgeRequestsAndOrder(t *testing.T) {
	client, script := agenttest.ScriptClient(t, validTurn(), validTurn())
	j := newTestJudge(t, client)
	first, err := j.Judge(t.Context(), testInput())
	require.NoError(t, err)
	second, err := j.Judge(t.Context(), testInput())
	require.NoError(t, err)
	assert.Equal(t, first.Judgments, second.Judgments)
	assert.Equal(t, []Judgment{{ClaimID: "a", Label: Entailed, Rationale: "The subject says blue."}, {ClaimID: "b", Label: Contradicted, Rationale: "The sky is blue."}}, first.Judgments)
	reqs := script.Requests()
	require.Len(t, reqs, 2)
	a, err := callrecord.Encode(reqs[0])
	require.NoError(t, err)
	b, err := callrecord.Encode(reqs[1])
	require.NoError(t, err)
	assert.Equal(t, a, b)
	golden(t, "request.json", a)
}

func TestJudgeNoCall(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Input)
		limit int
		want  error
	}{
		{"no claims", func(in *Input) {
			in.Claims = nil
		}, 0, ErrInvalidInput},
		{"bad ID", func(in *Input) {
			in.Claims[0].ID = "Bad"
		}, 0, ErrInvalidInput},
		{"UTF8 ID", func(in *Input) {
			in.Claims[0].ID = "\xff"
		}, 0, ErrInvalidInput},
		{"UTF8 text", func(in *Input) {
			in.Claims[0].Text = "\xff"
		}, 0, ErrInvalidInput},
		{"duplicate", func(in *Input) {
			in.Claims[1].ID = "a"
		}, 0, ErrInvalidInput},
		{"empty claim", func(in *Input) {
			in.Claims[0].Text = ""
		}, 0, ErrInvalidInput},
		{"bad subject", func(in *Input) {
			in.Subject = "Bad"
		}, 0, ErrInvalidInput},
		{"input limit", func(in *Input) {
			in.Text = strings.Repeat("x", 4096)
		}, 4096, ErrInputTooLarge},
		{"schema limit", func(in *Input) {
			in.Claims = make([]Claim, 5000)
			for i := range in.Claims {
				in.Claims[i] = Claim{ID: fmt.Sprintf("claim_%04d", i), Text: "claim"}
			}
		}, 0, ErrSchemaTooLarge},
		{"empty subject", func(in *Input) {
			in.Text = " \t\n"
			in.Truncated = true
		}, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, script := agenttest.ScriptClient(t)
			j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, MaxInputBytes: tt.limit})
			require.NoError(t, err)
			in := testInput()
			tt.edit(&in)
			result, err := j.Judge(t.Context(), in)
			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.True(t, result.NoCall)
				assert.True(t, result.Truncated)
				require.Len(t, result.Judgments, 2)
				assert.Equal(t, NotAddressed, result.Judgments[0].Label)
				assert.Equal(t, NotAddressed, result.Judgments[1].Label)
			}
			assert.Empty(t, script.Requests())
		})
	}
}

func TestRawCorrections(t *testing.T) {
	tests := []struct{ name, payload, tool, diagnostic string }{
		{"duplicate", `{"a":{},"a":{}}`, toolName, "duplicate"},
		{"escaped duplicate", `{"a":{},"\u0061":{}}`, toolName, "duplicate"},
		{"unknown claim", `{"a":{"label":"entailed","rationale":"ok"},"x":{"label":"entailed","rationale":"ok"}}`, toolName, "unknown claim"},
		{"missing claim", `{"a":{"label":"entailed","rationale":"ok"}}`, toolName, "missing claim"},
		{"bad label", `{"a":{"label":"yes","rationale":"ok"},"b":{"label":"entailed","rationale":"ok"}}`, toolName, "invalid label"},
		{"empty rationale", `{"a":{"label":"entailed","rationale":""},"b":{"label":"entailed","rationale":"ok"}}`, toolName, "requires a non-empty UTF-8 rationale"},
		{"UTF8 rationale", "{\"a\":{\"label\":\"entailed\",\"rationale\":\"\xff\"},\"b\":{}}", toolName, "UTF-8"},
		{"null rationale", `{"a":{"label":"entailed","rationale":null},"b":{}}`, toolName, "requires a non-empty UTF-8 rationale"},
		{"unknown member", `{"a":{"label":"entailed","rationale":"ok","extra":1},"b":{}}`, toolName, "unknown"},
		{"wrong tool", validPayload, "wrong_tool", "expected exactly one record_judgments tool call"},
		{"text only", "", "", "expected exactly one record_judgments tool call"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			turn := agenttest.Respond(agenttest.Text("no tool"))
			if tt.tool != "" {
				turn = agenttest.Respond(agenttest.RawToolCall(tools.Ident(tt.tool), rawjson.Message(tt.payload)))
			}
			script, err := agenttest.NewScript(turn, validTurn())
			require.NoError(t, err)
			j := newTestJudge(t, rawClient{script: script})
			result, err := j.Judge(t.Context(), testInput())
			require.NoError(t, err)
			assert.Equal(t, 1, result.Corrections)
			reqs := script.Requests()
			require.Len(t, reqs, 2)
			encoded, err := callrecord.Encode(reqs[1].Messages[len(reqs[0].Messages):])
			require.NoError(t, err)
			assert.Contains(t, string(encoded), tt.diagnostic)
			if tt.tool == toolName && tt.name != "duplicate" && tt.name != "escaped duplicate" && tt.name != "UTF8 rationale" {
				assert.Contains(t, string(encoded), `"IsError":true`)
				assert.Contains(t, string(encoded), `"Role":"assistant"`)
			} else {
				assert.NotContains(t, string(encoded), `"Kind":"tool_use"`)
			}
			assert.NoError(t, script.Err())
		})
	}
}

func TestCorrectionBoundAndProviderFailure(t *testing.T) {
	t.Run("bound", func(t *testing.T) {
		bad := agenttest.Respond(agenttest.Text("no tool"))
		script, err := agenttest.NewScript(bad, bad, bad, bad)
		require.NoError(t, err)
		j := newTestJudge(t, rawClient{script: script})
		_, err = j.Judge(t.Context(), testInput())
		require.ErrorIs(t, err, ErrInvalidOutput)
		assert.Len(t, script.Requests(), 4)
		assert.NoError(t, script.Err())
	})
	t.Run("provider", func(t *testing.T) {
		failure := errors.New("provider unavailable")
		client, script := agenttest.ScriptClient(t, agenttest.Fail(failure))
		j := newTestJudge(t, client)
		_, err := j.Judge(t.Context(), testInput())
		require.ErrorIs(t, err, failure)
		assert.Len(t, script.Requests(), 1)
	})
}

func TestValidatedCorrectionAndUsage(t *testing.T) {
	client, script := agenttest.ScriptClient(t,
		agenttest.Respond(agenttest.RawToolCall(toolName, rawjson.Message(`{"a":{"label":"SECRET","rationale":"hidden"}}`)), agenttest.Usage(10, 5)),
		agenttest.Respond(agenttest.RawToolCall(toolName, rawjson.Message(validPayload)), agenttest.Usage(20, 8)),
	)
	j := newTestJudge(t, client)
	in := testInput()
	in.Truncated = true
	result, err := j.Judge(t.Context(), in)
	require.NoError(t, err)
	assert.True(t, result.Truncated)
	assert.Equal(t, 1, result.Corrections)
	assert.Equal(t, 30, result.Usage.InputTokens)
	assert.Equal(t, 13, result.Usage.OutputTokens)
	assert.Equal(t, 43, result.Usage.TotalTokens)
	assert.Positive(t, result.Latency)
	reqs := script.Requests()
	require.Len(t, reqs, 2)
	require.Len(t, reqs[1].Messages, 3)
	correction := reqs[1].Messages[2]
	assert.Equal(t, model.ConversationRoleUser, correction.Role)
	encoded, err := callrecord.Encode(correction)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "tool_arguments")
	assert.NotContains(t, string(encoded), "SECRET")
}

func validTurn() agenttest.Turn {
	return agenttest.Respond(agenttest.RawToolCall(toolName, rawjson.Message(validPayload)))
}

func newTestJudge(t *testing.T, client model.Client) *Judge {
	t.Helper()
	cache := NewMemoryCache()
	j, err := New(Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: cache})
	require.NoError(t, err)
	require.NoError(t, cache.Store(&Calibration{Key: j.CalibrationKey(), Passed: true, Completed: time.Now().UTC()}))
	return j
}

func (c rawClient) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	return c.script.Complete(ctx, req)
}

func TestCorrectionRequestLimit(t *testing.T) {
	client, script := agenttest.ScriptClient(t, agenttest.Respond(agenttest.Text("no tool")))
	cache := NewMemoryCache()
	cfg := Config{Client: client, ModelID: "judge-model", MaxOutputTokens: 512, Cache: cache}
	probe, err := New(cfg)
	require.NoError(t, err)
	req, err := probe.request(testInput())
	require.NoError(t, err)
	encoded, err := callrecord.Encode(req)
	require.NoError(t, err)
	cfg.MaxInputBytes = len(encoded)
	j, err := New(cfg)
	require.NoError(t, err)
	require.NoError(t, cache.Store(&Calibration{Key: j.CalibrationKey(), Completed: time.Now().UTC(), Passed: true}))
	result, err := j.Judge(t.Context(), testInput())
	require.ErrorIs(t, err, ErrInputTooLarge)
	assert.Zero(t, result.Corrections)
	assert.Len(t, script.Requests(), 1)
}
