package control_test

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type executor struct {
	record  control.JobRecord
	accepts int
	failure error
}

func (e *executor) Accept(_ context.Context, s control.Submission) (control.JobRecord, error) {
	e.accepts++
	if e.failure != nil {
		return control.JobRecord{}, e.failure
	}
	if e.record.ID != "" {
		if e.record.Submission != s {
			return control.JobRecord{}, control.NewError(control.RequestConflict, "unsafe", "unsafe", "")
		}
		return e.record, nil
	}
	e.record = control.JobRecord{ID: "job-1", Submission: s, State: control.Queued}
	return e.record, nil
}
func (e *executor) Status(_ context.Context, scope, id string) (control.JobRecord, error) {
	if e.record.ID != id || e.record.Submission.Scope != scope {
		return control.JobRecord{}, control.NewError(control.NotFound, "unsafe", "unsafe", "")
	}
	return e.record, nil
}
func (e *executor) Cancel(ctx context.Context, scope, id string) (control.JobRecord, error) {
	r, err := e.Status(ctx, scope, id)
	if err != nil {
		return r, err
	}
	e.record.State = control.Canceled
	return e.record, nil
}

func config(t *testing.T) control.Config {
	t.Helper()
	binding := candidate.Binding{ID: "files", Version: "1"}
	cs, err := candidate.Open(t.Context(), t.TempDir(), candidate.Surface{ID: "sample", Version: "1", MaxTotalBytes: 2048, Slots: []candidate.Slot{{Name: "content", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put, candidate.Delete}, MediaTypes: []string{"text/plain"}, MaxBytes: 1024, MaxComponents: 4, Binding: binding}}}, []candidate.Component{{Name: "main.md", Slot: "content", MediaType: "text/plain", Data: []byte("baseline")}})
	require.NoError(t, err)
	es, err := eval.Open(t.TempDir())
	require.NoError(t, err)
	return control.Config{Scope: "owner", Role: inspect.Editor, Candidates: cs, Experiments: es, Tasks: []control.TaskBinding{{Task: control.Task{ID: "task", Modes: []eval.ExecutionKind{eval.ExecutionProbe}}, Snapshot: "task-v1", HostBinding: binding, ComponentBindings: []candidate.Binding{binding}}}, Executor: &executor{}, Operations: []control.Operation{control.DiscoverOperation, control.CreateOperation, control.GetOperation, control.ReadOperation, control.EditOperation, control.ValidateOperation, control.FreezeOperation, control.SubmitOperation, control.StatusOperation, control.CancelOperation}, Authorize: func(context.Context, control.Operation) error { return nil }, Limits: control.DefaultLimits()}
}
func service(t *testing.T, cfg control.Config) *control.Service {
	t.Helper()
	s, err := control.New(cfg)
	require.NoError(t, err)
	return s
}
func draft(t *testing.T, s *control.Service, cfg control.Config) control.CandidateInfo {
	t.Helper()
	d, err := s.Create(t.Context(), control.CreateRequest{Baseline: cfg.Candidates.Discover().BaselineDigest})
	require.NoError(t, err)
	return d
}
func code(t *testing.T, err error, want control.Code) {
	t.Helper()
	var ce *control.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, want, ce.Code)
	assert.NotEmpty(t, ce.Recovery)
}

func TestControlServiceRejectedEditRepair(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	d := draft(t, s, cfg)
	text := "new content"
	_, err := s.Edit(t.Context(), control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Put, Name: "main.md", Slot: "content", MediaType: "text/plain", Content: &text, Dependencies: []string{"missing"}})
	code(t, err, control.InvalidInput)
	current, err := s.GetCandidate(t.Context(), control.GetCandidateRequest{DraftID: d.DraftID})
	require.NoError(t, err)
	assert.Equal(t, d.Revision, current.Revision)
	d, err = s.Edit(t.Context(), control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Put, Name: "main.md", Slot: "content", MediaType: "text/plain", Content: &text})
	require.NoError(t, err)
	_, err = s.Validate(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision - 1})
	code(t, err, control.RevisionConflict)
	f, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
	require.NoError(t, err)
	got, err := s.ReadCandidate(t.Context(), control.ReadCandidateRequest{Candidate: f.Candidate, Component: "main.md", MaxBytes: 3})
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), got.Data)
	assert.True(t, got.Truncated)
	_, err = s.Edit(t.Context(), control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Delete, Name: "main.md"})
	code(t, err, control.FrozenConflict)
}
func TestControlTextAndBinaryEquivalent(t *testing.T) {
	cfg := config(t)
	s := service(t, cfg)
	text := "hello\n世界"
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	digests := make([]string, 0, 2)
	for _, binary := range []bool{false, true} {
		d := draft(t, s, cfg)
		q := control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Put, Name: "main.md", Slot: "content", MediaType: "text/plain"}
		if binary {
			q.ContentBase64 = &encoded
		} else {
			q.Content = &text
		}
		d, err := s.Edit(t.Context(), q)
		require.NoError(t, err)
		f, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision})
		require.NoError(t, err)
		digests = append(digests, f.Candidate)
	}
	assert.Equal(t, digests[0], digests[1])
}
func TestControlEditInputRejectsAmbiguity(t *testing.T) {
	for _, mode := range []string{"both", "neither", "utf8", "base64", "delete-content", "limit", "empty"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config(t)
			s := service(t, cfg)
			d := draft(t, s, cfg)
			text := ""
			encoded := ""
			q := control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Put, Name: "main.md", Slot: "content", MediaType: "text/plain"}
			switch mode {
			case "both":
				q.Content = &text
				q.ContentBase64 = &encoded
			case "utf8":
				text = string([]byte{255})
				q.Content = &text
			case "base64":
				encoded = "Zg"
				q.ContentBase64 = &encoded
			case "delete-content":
				q.Operation = candidate.Delete
				q.Content = &text
			case "limit":
				text = strings.Repeat("a", 1025)
				q.Content = &text
			case "empty":
				q.Content = &text
			}
			_, err := s.Edit(t.Context(), q)
			if mode == "empty" {
				require.NoError(t, err)
			} else {
				code(t, err, control.InvalidInput)
			}
		})
	}
}
func TestControlScopeGrantsAndSelectorDTO(t *testing.T) {
	cfg := config(t)
	cfg.Role = inspect.Selector
	cfg.Operations = []control.Operation{control.DiscoverOperation, control.StatusOperation}
	e := cfg.Executor.(*executor)
	e.record = control.JobRecord{ID: "job-1", Submission: control.Submission{Scope: "owner", Candidate: "secret-candidate", RequestKey: "secret-key"}, State: control.Failed, Failure: &control.Error{Code: control.OperationFailed, Message: "secret /host/path", Recovery: "secret"}}
	s := service(t, cfg)
	discovery, err := s.Discover(t.Context())
	require.NoError(t, err)
	assert.Nil(t, discovery.Surface)
	assert.Empty(t, discovery.Tasks)
	got, err := s.Status(t.Context(), control.JobRequest{JobID: "job-1"})
	require.NoError(t, err)
	data, err := json.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "secret")
	assert.NotContains(t, string(data), "candidate")
	assert.NotContains(t, string(data), "scope")
	_, err = s.Create(t.Context(), control.CreateRequest{Baseline: cfg.Candidates.Discover().BaselineDigest})
	code(t, err, control.Unsupported)
	cfg.Role = inspect.Editor
	cfg.Authorize = func(context.Context, control.Operation) error { return errors.New("secret policy") }
	s = service(t, cfg)
	_, err = s.Status(t.Context(), control.JobRequest{JobID: "job-1"})
	code(t, err, control.NotFound)
	assert.NotContains(t, err.Error(), "secret")
}
