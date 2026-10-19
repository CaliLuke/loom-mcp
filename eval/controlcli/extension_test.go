package controlcli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/controlcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestControlCLIProjectionParity(t *testing.T) {
	cfg := cliControlConfig(t)
	service, err := control.New(cfg)
	require.NoError(t, err)
	extension, err := controlcli.Extensions(func(_ context.Context, store *eval.Store) (*control.Service, error) {
		bound := cfg
		bound.Experiments = store
		return control.New(bound)
	})
	require.NoError(t, err)
	discovery, err := service.Discover(t.Context())
	require.NoError(t, err)
	code, out, diagnostic := invokeControlCLI(t, extension, "experiment", "discover")
	assert.Equal(t, 0, code, diagnostic)
	expected, err := json.Marshal(discovery)
	require.NoError(t, err)
	assert.JSONEq(t, string(expected), out)
	baseline := cfg.Candidates.Discover().BaselineDigest
	create := control.CreateRequest{Baseline: baseline}
	code, out, diagnostic = invokeControlCLI(t, extension, "experiment", "create_candidate", "--request", controlJSON(t, create))
	require.Equal(t, 0, code, diagnostic)
	var draft control.CandidateInfo
	require.NoError(t, json.Unmarshal([]byte(out), &draft))
	read := control.ReadCandidateRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, Component: "main.md", MaxBytes: 3}
	goRead, err := service.ReadCandidate(t.Context(), read)
	require.NoError(t, err)
	native, err := control.NewToolsetRegistration(control.ToolsetConfig{Service: service})
	require.NoError(t, err)
	result, err := native.Execute(t.Context(), &planner.ToolRequest{Name: tools.Ident("experiment.read_candidate"), ToolCallID: "native", Payload: rawjson.Message(controlJSON(t, read))})
	require.NoError(t, err)
	require.Nil(t, result.ToolResult.Error)
	assert.Equal(t, goRead, result.ToolResult.Result)
	code, out, diagnostic = invokeControlCLI(t, extension, "experiment", "read_candidate", "--request", controlJSON(t, read))
	require.Equal(t, 0, code, diagnostic)
	assert.JSONEq(t, controlJSON(t, goRead), out)
	for _, binary := range []bool{false, true} {
		text := "new\n世界"
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		edit := control.EditRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, Operation: candidate.Put, Name: "main.md", Slot: "content", MediaType: "text/plain"}
		if binary {
			edit.ContentBase64 = &encoded
		} else {
			edit.Content = &text
		}
		code, out, diagnostic = invokeControlCLI(t, extension, "experiment", "edit_candidate", "--request", controlJSON(t, edit))
		require.Equal(t, 0, code, diagnostic)
		require.NoError(t, json.Unmarshal([]byte(out), &draft))
	}
	updated, err := service.ReadCandidate(t.Context(), control.ReadCandidateRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, Component: "main.md"})
	require.NoError(t, err)
	assert.Equal(t, []byte("new\n世界"), updated.Data)
	stale := control.RevisionRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision - 1}
	_, goErr := service.Validate(t.Context(), stale)
	require.Error(t, goErr)
	nativeError, err := native.Execute(t.Context(), &planner.ToolRequest{Name: "experiment.validate_candidate", ToolCallID: "native-error", Payload: rawjson.Message(controlJSON(t, stale))})
	require.NoError(t, err)
	require.NotNil(t, nativeError.ToolResult.Error)
	assert.Equal(t, goErr.Error(), nativeError.ToolResult.Error.Message)
	code, out, diagnostic = invokeControlCLI(t, extension, "experiment", "validate_candidate", "--request", controlJSON(t, stale))
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, goErr.Error()+"\n", diagnostic)
	revision := control.RevisionRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision}
	for _, op := range []string{"get_candidate", "validate_candidate", "freeze_candidate"} {
		payload := controlJSON(t, revision)
		if op == "get_candidate" {
			payload = controlJSON(t, control.GetCandidateRequest{DraftID: draft.DraftID})
		}
		code, _, diagnostic = invokeControlCLI(t, extension, "experiment", op, "--request", payload)
		assert.Equal(t, 0, code, diagnostic)
	}
}

func TestControlCLIRejectsClosedInvalidRequests(t *testing.T) {
	cfg := cliControlConfig(t)
	extensions, err := controlcli.Extensions(func(_ context.Context, s *eval.Store) (*control.Service, error) {
		cfg.Experiments = s
		return control.New(cfg)
	})
	require.NoError(t, err)
	for _, args := range [][]string{
		{}, {"bogus"}, {"create_candidate"}, {"discover", "--request", "[]"}, {"discover", "--request", "null"},
		{"discover", "--request", `{"scope":"injected"}`}, {"discover", "--request", `{"live":true}`},
		{"edit_candidate", "--request", `{"draft_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_revision":1,"operation":"put","name":"main.md","slot":"content","media_type":"text/plain","content":"text","content_base64":"dGV4dA=="}`},
		{"edit_candidate", "--request", `{"draft_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_revision":1,"operation":"put","name":"main.md","slot":"content","media_type":"text/plain"}`},
		{"discover", "--request", "@file"}, {"discover", "--request", "{} {}"}, {"discover", "--request", "{}", "--request", "{}"},
		{"read_candidate", "--request", `{"component":"main.md","offset":-1}`},
	} {
		code, out, diagnostic := invokeControlCLI(t, extensions, append([]string{"experiment"}, args...)...)
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		var failure control.Error
		require.NoError(t, json.Unmarshal([]byte(diagnostic), &failure))
		assert.Equal(t, control.InvalidInput, failure.Code)
		assert.NotEmpty(t, failure.Recovery)
	}
	_, err = controlcli.Extensions(nil)
	require.Error(t, err)
}

func TestControlCLISelectedStoreHelpAndDelimiter(t *testing.T) {
	cfg := cliControlConfig(t)
	selected := t.TempDir()
	fallback := t.TempDir()
	var admitted string
	extensions, err := controlcli.Extensions(func(ctx context.Context, s *eval.Store) (*control.Service, error) {
		writer, err := s.BeginProbe(ctx, eval.ProbeConfig{Key: eval.AttemptKey{Variant: "one", Scenario: "task", Item: "one", Repeat: 1}, Item: eval.Item{ID: "one", Input: "hello"}, Variant: eval.Variant{ID: "one"}, PrepareAttempt: func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
			return nil, nil
		}, PrepareTimeout: time.Second, CleanupTimeout: time.Second}, eval.ExperimentMeta{})
		if err != nil {
			return nil, err
		}
		admitted = writer.ID()
		cfg.Experiments = s
		return control.New(cfg)
	})
	require.NoError(t, err)
	var out, diagnostic bytes.Buffer
	cli := evalcli.Config{StoreDir: fallback, Extensions: extensions, Stdout: &out, Stderr: &diagnostic}
	assert.Equal(t, 0, evalcli.Main(t.Context(), []string{"--out", selected, "experiment", "discover"}, cli), diagnostic.String())
	store, err := eval.Open(selected)
	require.NoError(t, err)
	_, err = store.Load(admitted)
	require.NoError(t, err)
	other, err := eval.Open(fallback)
	require.NoError(t, err)
	_, err = other.Load(admitted)
	require.Error(t, err)
	code, help, stderr := invokeControlCLI(t, extensions, "help", "experiment")
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, help, "--request")
	assert.Contains(t, help, "--out")
	code, _, stderr = invokeControlCLI(t, extensions, "experiment", "--", "discover", "--request", "{}")
	assert.Equal(t, 0, code, stderr)
	code, _, stderr = invokeControlCLI(t, extensions, "experiment", "--", "discover", "--out", "literal")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid_input")
	code, _, _ = invokeControlCLI(t, nil, "experiment", "discover")
	assert.Equal(t, 2, code)
}

func TestControlCLIBindErrorsAreSafe(t *testing.T) {
	for _, failure := range []error{errors.New("secret /host/credentials"), &control.Error{Code: control.NotFound, Message: "secret", Recovery: "secret"}} {
		extensions, err := controlcli.Extensions(func(context.Context, *eval.Store) (*control.Service, error) {
			return nil, failure
		})
		require.NoError(t, err)
		code, _, diagnostic := invokeControlCLI(t, extensions, "experiment", "discover")
		assert.Equal(t, 1, code)
		assert.NotContains(t, diagnostic, "secret")
		assert.Contains(t, diagnostic, "recovery")
	}
}

func TestControlCLIJoinedCancellationDoesNotLeakHostErrors(t *testing.T) {
	extensions, err := controlcli.Extensions(func(context.Context, *eval.Store) (*control.Service, error) {
		return nil, errors.Join(context.Canceled, errors.New("secret /host/credentials"))
	})
	require.NoError(t, err)
	code, _, diagnostic := invokeControlCLI(t, extensions, "experiment", "discover")
	assert.Equal(t, 1, code)
	assert.Equal(t, context.Canceled.Error()+"\n", diagnostic)
}

func cliControlConfig(t *testing.T) control.Config {
	t.Helper()
	binding := candidate.Binding{ID: "files", Version: "1"}
	candidates, err := candidate.Open(t.Context(), t.TempDir(), candidate.Surface{ID: "cli", Version: "1", MaxTotalBytes: 4096, Slots: []candidate.Slot{{Name: "content", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/plain"}, MaxBytes: 2048, MaxComponents: 4, Binding: binding}}}, []candidate.Component{{Name: "main.md", Slot: "content", MediaType: "text/plain", Data: []byte("baseline content")}})
	require.NoError(t, err)
	experiments, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	return control.Config{Scope: "cli", Role: inspect.Editor, Candidates: candidates, Experiments: experiments, Operations: []control.Operation{control.DiscoverOperation, control.CreateOperation, control.GetOperation, control.ReadOperation, control.EditOperation, control.ValidateOperation, control.FreezeOperation}, Authorize: func(context.Context, control.Operation) error {
		return nil
	}, Limits: control.DefaultLimits()}
}

func invokeControlCLI(t *testing.T, extensions []evalcli.Extension, args ...string) (int, string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := evalcli.Main(t.Context(), args, evalcli.Config{StoreDir: t.TempDir(), Extensions: extensions, Stdout: &out, Stderr: &diagnostic})
	return code, out.String(), diagnostic.String()
}

func controlJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return strings.TrimSpace(string(raw))
}
