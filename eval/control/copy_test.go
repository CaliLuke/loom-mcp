package control_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlCopyPreviewConfirmationAndOrigins(t *testing.T) {
	cfg := copyConfig(t)
	source := copySource(t, cfg)
	s := service(t, cfg)
	d := draft(t, s, cfg)
	request := control.PreviewCopyRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, SourceCandidate: source.Digest(), Components: []string{"main.md"}}
	plan, err := s.PreviewCopy(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, []string{"main.md"}, plan.Selected)
	assert.Equal(t, []string{"main.md"}, plan.Replace)
	require.Len(t, plan.Closure, 2)
	assert.Equal(t, "helper.txt", plan.Closure[0].Name)
	current, err := s.GetCandidate(t.Context(), control.GetCandidateRequest{DraftID: d.DraftID})
	require.NoError(t, err)
	assert.Equal(t, d, current)
	confirm := confirmCopy(plan)
	confirm.PlanDigest = strings.Repeat("a", 64)
	_, err = s.Copy(t.Context(), confirm)
	code(t, err, control.RevisionConflict)
	assert.Contains(t, err.Error(), "preview")
	assert.Contains(t, err.Error(), "plan_digest")
	result, err := s.Copy(t.Context(), confirmCopy(plan))
	require.NoError(t, err)
	assert.Equal(t, d.Revision+1, result.Candidate.Revision)
	assert.Equal(t, []string{"helper.txt", "main.md"}, result.Closure)
	require.Len(t, result.Origins, 2)
	assert.Equal(t, source.Digest(), result.Origins[0].SourceCandidate)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "procedure bytes")
	frozen, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: result.Candidate.Revision})
	require.NoError(t, err)
	assert.Equal(t, 2, frozen.Manifest.Version)
	assert.Equal(t, result.Origins, frozen.Manifest.Origins)
}

func TestControlCopyRechecksCurrentOperationGrants(t *testing.T) {
	for _, op := range []control.Operation{control.ReadOperation, control.EditOperation, control.PreviewCopyOperation, control.CopyOperation} {
		t.Run(string(op), func(t *testing.T) {
			cfg := copyConfig(t)
			source := copySource(t, cfg)
			var denied control.Operation
			cfg.Authorize = func(_ context.Context, operation control.Operation) error {
				if operation == denied {
					return errors.New("secret host policy")
				}
				return nil
			}
			s := service(t, cfg)
			d := draft(t, s, cfg)
			q := control.PreviewCopyRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, SourceCandidate: source.Digest(), Components: []string{"main.md"}}
			plan, err := s.PreviewCopy(t.Context(), q)
			require.NoError(t, err)
			denied = op
			if op != control.CopyOperation {
				_, err = s.PreviewCopy(t.Context(), q)
				code(t, err, control.NotFound)
				assert.NotContains(t, err.Error(), "secret")
			}
			if op != control.PreviewCopyOperation {
				_, err = s.Copy(t.Context(), confirmCopy(plan))
				code(t, err, control.NotFound)
				assert.NotContains(t, err.Error(), "secret")
			}
			current, err := cfg.Candidates.Draft(t.Context(), d.DraftID)
			require.NoError(t, err)
			assert.Equal(t, d.Revision, current.Revision)
			assert.Empty(t, current.Origins)
		})
	}
}

func TestControlCopyRejectsClosedInputsAndBoundedPreview(t *testing.T) {
	cfg := copyConfig(t)
	source := copySource(t, cfg)
	s := service(t, cfg)
	d := draft(t, s, cfg)
	base := control.PreviewCopyRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, SourceCandidate: source.Digest(), Components: []string{"main.md"}}
	for _, which := range []string{"draft", "revision", "source", "components"} {
		q := base
		switch which {
		case "draft":
			q.DraftID = "../host"
		case "revision":
			q.ExpectedRevision = 0
		case "source":
			q.SourceCandidate = "../host"
		case "components":
			q.Components = nil
		}
		_, err := s.PreviewCopy(t.Context(), q)
		code(t, err, control.InvalidInput)
	}
	missing := base
	missing.SourceCandidate = strings.Repeat("a", 64)
	_, err := s.PreviewCopy(t.Context(), missing)
	code(t, err, control.NotFound)
	for _, limit := range []string{"entries", "bytes"} {
		bounded := cfg
		if limit == "entries" {
			bounded.Limits.MaxPageEntries = 1
		} else {
			bounded.Limits.MaxReadBytes = 1
		}
		limited := service(t, bounded)
		_, err := limited.PreviewCopy(t.Context(), base)
		code(t, err, control.InvalidInput)
		current, err := cfg.Candidates.Draft(t.Context(), d.DraftID)
		require.NoError(t, err)
		assert.Equal(t, d.Revision, current.Revision)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.PreviewCopy(ctx, base)
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Copy(ctx, control.CopyRequest{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestControlCopyNativeParityAndClosedSchemas(t *testing.T) {
	cfg := copyConfig(t)
	source := copySource(t, cfg)
	s := service(t, cfg)
	reg := native(t, s)
	d := draft(t, s, cfg)
	q := control.PreviewCopyRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, SourceCandidate: source.Digest(), Components: []string{"main.md"}}
	plan, err := s.PreviewCopy(t.Context(), q)
	require.NoError(t, err)
	out := invoke(t, reg, control.PreviewCopyOperation, q)
	require.Nil(t, out.Error)
	assert.Equal(t, plan, out.Result)
	confirm := confirmCopy(plan)
	bad := confirm
	bad.PlanDigest = strings.Repeat("a", 64)
	_, goError := s.Copy(t.Context(), bad)
	nativeError := invoke(t, reg, control.CopyOperation, bad)
	require.NotNil(t, nativeError.Error)
	assert.Equal(t, goError.Error(), nativeError.Error.Message)
	assert.Contains(t, strings.ToLower(nativeError.RetryHint.Message), "preview")
	result := invoke(t, reg, control.CopyOperation, confirm)
	require.Nil(t, result.Error)
	copied, ok := result.Result.(control.CopyResult)
	require.True(t, ok)
	assert.Equal(t, plan.Digest, copied.PlanDigest)
	frozen := invoke(t, reg, control.FreezeOperation, control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: copied.Candidate.Revision})
	require.Nil(t, frozen.Error)
	outputs := map[control.Operation]any{control.PreviewCopyOperation: plan, control.CopyOperation: copied, control.FreezeOperation: frozen.Result}
	for _, spec := range reg.Specs {
		op := control.Operation(strings.TrimPrefix(spec.Name.String(), "experiment."))
		if value, exists := outputs[op]; exists {
			encoded, err := spec.Result.Codec.ToJSON(value)
			require.NoError(t, err)
			var decoded any
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.NoError(t, tools.ValidateJSONSchema(spec.Result.Schema, decoded))
			_, err = spec.Result.Codec.FromJSON(encoded)
			require.NoError(t, err)
		}
	}
	validRaw, err := json.Marshal(confirm)
	require.NoError(t, err)
	var valid map[string]any
	require.NoError(t, json.Unmarshal(validRaw, &valid))
	for _, field := range []string{"role", "scope", "root", "credentials", "live", "origin"} {
		injected := make(map[string]any, len(valid)+1)
		for k, v := range valid {
			injected[k] = v
		}
		injected[field] = "injected"
		out := invoke(t, reg, control.CopyOperation, injected)
		require.NotNil(t, out.Error)
		assert.Contains(t, out.Error.Message, "invalid_input")
	}
	delete(valid, "replace")
	noConfirmation := invoke(t, reg, control.CopyOperation, valid)
	require.NotNil(t, noConfirmation.Error)
	assert.Contains(t, noConfirmation.Error.Message, "invalid_input")
}

func TestControlCopySelectorCannotReceiveAuthorToolsOrOrigins(t *testing.T) {
	cfg := copyConfig(t)
	cfg.Role = inspect.Selector
	_, err := control.New(cfg)
	code(t, err, control.InvalidInput)
	cfg.Operations = []control.Operation{control.DiscoverOperation}
	s := service(t, cfg)
	reg := native(t, s)
	require.Len(t, reg.Specs, 1)
	discovery, err := s.Discover(t.Context())
	require.NoError(t, err)
	assert.Nil(t, discovery.Surface)
	_, err = s.PreviewCopy(t.Context(), control.PreviewCopyRequest{})
	code(t, err, control.Unsupported)
	_, err = s.Copy(t.Context(), control.CopyRequest{})
	code(t, err, control.Unsupported)
}

func copyConfig(t *testing.T) control.Config {
	cfg := config(t)
	cfg.Operations = append(cfg.Operations, control.PreviewCopyOperation, control.CopyOperation)
	return cfg
}

func copySource(t *testing.T, cfg control.Config) *candidate.Frozen {
	t.Helper()
	d, err := cfg.Candidates.Create(t.Context())
	require.NoError(t, err)
	d, err = cfg.Candidates.Edit(t.Context(), d.ID, d.Revision, []candidate.Edit{
		{Operation: candidate.Put, Component: candidate.Component{Name: "main.md", Slot: "content", MediaType: "text/plain", Data: []byte("procedure bytes"), Dependencies: []string{"helper.txt"}}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "helper.txt", Slot: "content", MediaType: "text/plain", Data: []byte("helper bytes")}},
		{Operation: candidate.Put, Component: candidate.Component{Name: "unrelated.txt", Slot: "content", MediaType: "text/plain", Data: []byte("not imported")}},
	})
	require.NoError(t, err)
	f, err := cfg.Candidates.Freeze(t.Context(), d.ID, d.Revision)
	require.NoError(t, err)
	return f
}

func confirmCopy(p candidate.CopyPlan) control.CopyRequest {
	return control.CopyRequest{DraftID: p.DraftID, ExpectedRevision: p.Revision, SourceCandidate: p.SourceCandidate, Components: p.Selected, Replace: p.Replace, PlanDigest: p.Digest}
}
