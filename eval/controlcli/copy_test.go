package controlcli_test

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/controlcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlCLICopyProjectionParity(t *testing.T) {
	cfg := cliControlConfig(t)
	cfg.Operations = append(cfg.Operations, control.PreviewCopyOperation, control.CopyOperation)
	source := cliCopySource(t, cfg)
	service, err := control.New(cfg)
	require.NoError(t, err)
	extensions, err := controlcli.Extensions(func(_ context.Context, store *eval.Store) (*control.Service, error) {
		bound := cfg
		bound.Experiments = store
		return control.New(bound)
	})
	require.NoError(t, err)
	d, err := service.Create(t.Context(), control.CreateRequest{Baseline: cfg.Candidates.Discover().BaselineDigest})
	require.NoError(t, err)
	preview := control.PreviewCopyRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, SourceCandidate: source.Digest(), Components: []string{"main.md"}}
	goPlan, err := service.PreviewCopy(t.Context(), preview)
	require.NoError(t, err)
	exit, out, diagnostic := invokeControlCLI(t, extensions, "experiment", "preview_copy", "--request", controlJSON(t, preview))
	require.Equal(t, 0, exit, diagnostic)
	assert.JSONEq(t, controlJSON(t, goPlan), out)
	unchanged, err := service.GetCandidate(t.Context(), control.GetCandidateRequest{DraftID: d.DraftID})
	require.NoError(t, err)
	assert.Equal(t, d, unchanged)
	confirm := control.CopyRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, SourceCandidate: source.Digest(), Components: goPlan.Selected, Replace: goPlan.Replace, PlanDigest: strings.Repeat("a", 64)}
	_, goErr := service.Copy(t.Context(), confirm)
	require.Error(t, goErr)
	registration, err := control.NewToolsetRegistration(control.ToolsetConfig{Service: service})
	require.NoError(t, err)
	native, err := registration.Execute(t.Context(), &planner.ToolRequest{Name: "experiment.copy_candidate", ToolCallID: "copy", Payload: rawjson.Message(controlJSON(t, confirm))})
	require.NoError(t, err)
	require.NotNil(t, native.ToolResult.Error)
	assert.Equal(t, goErr.Error(), native.ToolResult.Error.Message)
	exit, out, diagnostic = invokeControlCLI(t, extensions, "experiment", "copy_candidate", "--request", controlJSON(t, confirm))
	assert.Equal(t, 1, exit)
	assert.Empty(t, out)
	assert.Equal(t, goErr.Error()+"\n", diagnostic)
	confirm.PlanDigest = goPlan.Digest
	exit, out, diagnostic = invokeControlCLI(t, extensions, "experiment", "copy_candidate", "--request", controlJSON(t, confirm))
	require.Equal(t, 0, exit, diagnostic)
	var copied control.CopyResult
	require.NoError(t, json.Unmarshal([]byte(out), &copied))
	current, err := service.GetCandidate(t.Context(), control.GetCandidateRequest{DraftID: d.DraftID})
	require.NoError(t, err)
	assert.Equal(t, current, copied.Candidate)
	assert.Equal(t, []string{"helper.txt", "main.md"}, copied.Closure)
	assert.Equal(t, goPlan.Digest, copied.PlanDigest)
	require.Len(t, copied.Origins, 2)
	assert.Equal(t, source.Digest(), copied.Origins[0].SourceCandidate)
	assert.NotContains(t, out, "procedure bytes")
}

func TestControlCLICopyUnavailableToSelector(t *testing.T) {
	cfg := cliControlConfig(t)
	cfg.Role = inspect.Selector
	cfg.Operations = []control.Operation{control.DiscoverOperation}
	extensions, err := controlcli.Extensions(func(_ context.Context, store *eval.Store) (*control.Service, error) {
		cfg.Experiments = store
		return control.New(cfg)
	})
	require.NoError(t, err)
	exit, out, diagnostic := invokeControlCLI(t, extensions, "experiment", "copy_candidate", "--request", `{}`)
	assert.Equal(t, 1, exit)
	assert.Empty(t, out)
	assert.Contains(t, diagnostic, "unsupported")
	assert.NotContains(t, diagnostic, "origin")
}

func cliCopySource(t *testing.T, cfg control.Config) *candidate.Frozen {
	t.Helper()
	d, err := cfg.Candidates.Create(t.Context())
	require.NoError(t, err)
	d, err = cfg.Candidates.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{
		{Operation: candidate.Put, Component: candidate.Component{Name: "main.md", Slot: "content", MediaType: "text/plain", Data: []byte("procedure bytes"), Dependencies: []string{"helper.txt"}}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "helper.txt", Slot: "content", MediaType: "text/plain", Data: []byte("helper")}},
	})
	require.NoError(t, err)
	frozen, err := cfg.Candidates.Freeze(t.Context(), d.ID, d.Revision)
	require.NoError(t, err)
	return frozen
}
