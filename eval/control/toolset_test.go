package control_test

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func native(t *testing.T, s *control.Service) agentsruntime.ToolsetRegistration {
	t.Helper()
	reg, err := control.NewToolsetRegistration(control.ToolsetConfig{Service: s})
	require.NoError(t, err)
	assert.False(t, reg.Inline)
	return reg
}
func invoke(t *testing.T, reg agentsruntime.ToolsetRegistration, op control.Operation, q any) *planner.ToolResult {
	t.Helper()
	raw, err := json.Marshal(q)
	require.NoError(t, err)
	out, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: tools.Ident(reg.Name + "." + string(op)), ToolCallID: "call", Payload: rawjson.Message(raw)})
	require.NoError(t, err)
	require.NotNil(t, out)
	return out.ToolResult
}
func TestControlNativeClosedSchemaInjection(t *testing.T) {
	cfg := config(t)
	cfg.Operations = append(cfg.Operations, control.ListEvidenceOperation, control.ReadEvidenceOperation, control.CompareOperation)
	cfg.Evidence = func(context.Context, string) (*inspect.View, error) {
		return nil, control.NewError(control.EvidenceNotReady, "", "", "")
	}
	s := service(t, cfg)
	reg := native(t, s)
	for _, spec := range reg.Specs {
		t.Run(spec.Name.String(), func(t *testing.T) {
			for _, field := range []string{"role", "scope", "root", "credentials", "live", "policy", "manifest", "raw_export"} {
				out, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: spec.Name, ToolCallID: "bad", Payload: rawjson.Message(`{"` + field + `":"injected"}`)})
				require.NoError(t, err)
				require.NotNil(t, out.ToolResult.Error)
				assert.NotNil(t, out.ToolResult.RetryHint)
				assert.NotEmpty(t, out.ToolResult.RetryHint.Message)
			}
		})
	}
}
func TestControlNativeEditParityAndSchema(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	reg := native(t, s)
	d := draft(t, s, cfg)
	base := map[string]any{"draft_id": d.DraftID, "expected_revision": d.Revision, "operation": "put", "name": "main.md", "slot": "content", "media_type": "text/plain"}
	for _, mode := range []string{"neither", "both", "null", "text"} {
		q := map[string]any{}
		for k, v := range base {
			q[k] = v
		}
		switch mode {
		case "both":
			q["content"] = "new"
			q["content_base64"] = "bmV3"
		case "null":
			q["content"] = nil
		case "text":
			q["content"] = "new"
		}
		out := invoke(t, reg, control.EditOperation, q)
		if mode == "text" {
			require.Nil(t, out.Error)
			got, ok := out.Result.(control.CandidateInfo)
			require.True(t, ok)
			assert.Equal(t, d.Revision+1, got.Revision)
		} else {
			require.NotNil(t, out.Error)
		}
	}
	out := invoke(t, reg, control.ReadOperation, map[string]any{"draft_id": d.DraftID, "expected_revision": d.Revision + 1, "component": "main.md", "max_bytes": 2})
	require.Nil(t, out.Error)
	read, ok := out.Result.(control.CandidateRead)
	require.True(t, ok)
	assert.Equal(t, []byte("ne"), read.Data)
	for _, spec := range reg.Specs {
		if spec.Name == "experiment.read_candidate" {
			encoded, err := spec.Result.Codec.ToJSON(read)
			require.NoError(t, err)
			var data any
			require.NoError(t, json.Unmarshal(encoded, &data))
			assert.NoError(t, tools.ValidateJSONSchema(spec.Result.Schema, data))
		}
	}
}
func TestControlNativeErrorsNeverPublishCauses(t *testing.T) {
	cfg := config(t)
	cfg.Authorize = func(context.Context, control.Operation) error {
		return control.NewError(control.NotFound, "/secret/root", "read /secret/root", "")
	}
	s := service(t, cfg)
	out := invoke(t, native(t, s), control.DiscoverOperation, struct{}{})
	require.NotNil(t, out.Error)
	assert.NotContains(t, out.Error.Message, "secret")
	assert.Nil(t, out.Error.Cause)
	var safe control.Error
	require.NoError(t, json.Unmarshal([]byte(out.Error.Message), &safe))
	assert.Equal(t, control.NotFound, safe.Code)
}
func TestControlNativeResultSchemas(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	reg := native(t, s)
	discovery := invoke(t, reg, control.DiscoverOperation, struct{}{})
	require.Nil(t, discovery.Error)
	created := invoke(t, reg, control.CreateOperation, control.CreateRequest{Baseline: cfg.Candidates.Discover().BaselineDigest})
	require.Nil(t, created.Error)
	d := created.Result.(control.CandidateInfo)
	cases := []struct {
		op control.Operation
		q  any
	}{{control.GetOperation, control.GetCandidateRequest{DraftID: d.DraftID}}, {control.ValidateOperation, control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision}}, {control.FreezeOperation, control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision}}}
	outputs := map[control.Operation]any{control.DiscoverOperation: discovery.Result, control.CreateOperation: created.Result}
	var frozen control.Frozen
	for _, tc := range cases {
		out := invoke(t, reg, tc.op, tc.q)
		require.Nil(t, out.Error)
		outputs[tc.op] = out.Result
		if tc.op == control.FreezeOperation {
			frozen = out.Result.(control.Frozen)
		}
	}
	job := invoke(t, reg, control.SubmitOperation, control.SubmitRequest{Candidate: frozen.Candidate, TaskID: "task", RequestKey: "key"})
	require.Nil(t, job.Error)
	outputs[control.SubmitOperation] = job.Result
	for _, spec := range reg.Specs {
		op := control.Operation(strings.TrimPrefix(spec.Name.String(), "experiment."))
		value, ok := outputs[op]
		if !ok {
			continue
		}
		t.Run(string(op), func(t *testing.T) {
			encoded, err := spec.Result.Codec.ToJSON(value)
			require.NoError(t, err)
			var instance any
			require.NoError(t, json.Unmarshal(encoded, &instance))
			assert.NoError(t, tools.ValidateJSONSchema(spec.Result.Schema, instance))
			_, err = spec.Result.Codec.FromJSON(encoded)
			assert.NoError(t, err)
		})
	}
}
