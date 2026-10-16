package runtime

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInlineToolPayloadAdapterControlsExecutionInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adapted jsontext.Value
		err     error
		want    string
	}{
		{"replace", jsontext.Value(`{"query":"private scoped"}`), nil, `{"query":"private scoped"}`},
		{"preserve", nil, nil, `{"query":"public"}`},
		{"failure", nil, assert.AnError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithoutContextDefault("content.agent"))
			spec := newAnyJSONSpec("inline.read", "inline")
			var actual rawjson.Message
			var metadata ToolCallMeta
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{Name: "inline", Inline: true, Specs: []tools.ToolSpec{spec},
				PayloadAdapter: func(_ context.Context, meta ToolCallMeta, name tools.Ident, raw jsontext.Value) (jsontext.Value, error) {
					metadata = meta
					assert.Equal(t, spec.Name, name)
					assert.JSONEq(t, `{"query":"public"}`, string(raw))
					return tc.adapted, tc.err
				}, Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
					actual = call.Payload
					return Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]bool{"done": true}}), nil
				}}))
			registerContentAgent(t, rt, &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
				return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{"query":"public"}`)}}}, nil
			}, resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
				return assistantCommitResult(), nil
			}})
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", nil, WithRunID("root"))
			assert.Equal(t, "root", metadata.RunID)
			assert.Equal(t, "session", metadata.SessionID)
			assert.Equal(t, "call", metadata.ToolCallID)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Empty(t, actual, "adapter failure must prevent tool execution")
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(actual))
		})
	}
}
