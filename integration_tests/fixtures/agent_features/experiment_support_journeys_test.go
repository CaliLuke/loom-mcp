package agentfeatures_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

func supportCreate(t *testing.T, env *supportEnvironment) control.CandidateInfo {
	t.Helper()
	discovery := supportMust[control.Discovery](t, env, control.DiscoverOperation, struct{}{})
	require.NotNil(t, discovery.Surface)
	return supportMust[control.CandidateInfo](t, env, control.CreateOperation, control.CreateRequest{Baseline: discovery.Surface.BaselineDigest})
}

func supportRevision(t *testing.T, env *supportEnvironment, section string) control.Frozen {
	t.Helper()
	draft := supportCreate(t, env)
	draft = supportEdit(t, env, draft, "helper.go", "helpers", "text/x-go", supportProcedureHelper, nil)
	draft = supportEdit(t, env, draft, "SKILL.md", "skills", "text/markdown", supportReusableSkill, []string{"helper.go"})
	draft = supportEdit(t, env, draft, "children/reviewer.md", "children", "text/plain", "section="+section+"\n", nil)
	return supportFreeze(t, env, draft)
}

func supportComposition(t *testing.T, env *supportEnvironment, task string) (supportRun, supportRun, supportRun) {
	t.Helper()
	first := supportCreate(t, env)
	first = supportEdit(t, env, first, "helper.go", "helpers", "text/x-go", supportProcedureHelper, nil)
	first = supportEdit(t, env, first, "SKILL.md", "skills", "text/markdown", supportReusableSkill, []string{"helper.go"})
	firstRun := supportExecute(t, env, supportFreeze(t, env, first), task)
	second := supportCreate(t, env)
	second = supportEdit(t, env, second, "children/reviewer.md", "children", "text/plain", "section=Audit\n", nil)
	secondRun := supportExecute(t, env, supportFreeze(t, env, second), task)
	combined := supportCreate(t, env)
	combined = supportCopy(t, env, combined, firstRun.frozen.Candidate, []string{"SKILL.md"})
	combined = supportCopy(t, env, combined, secondRun.frozen.Candidate, []string{"children/reviewer.md"})
	return firstRun, secondRun, supportExecute(t, env, supportFreeze(t, env, combined), task)
}

func supportTaskB(t *testing.T, env *supportEnvironment, selected control.Frozen, task string) supportRun {
	t.Helper()
	draft := supportCreate(t, env)
	for name, baseline := range map[string]string{"helper.go": supportBaselineHelper, "SKILL.md": supportSkill, "children/reviewer.md": supportChild} {
		read := supportMust[control.CandidateRead](t, env, control.ReadOperation, control.ReadCandidateRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, Component: name, MaxBytes: 16384})
		assert.Equal(t, baseline, string(read.Data))
		if name != "children/reviewer.md" {
			source := supportMust[control.CandidateRead](t, env, control.ReadOperation, control.ReadCandidateRequest{Candidate: selected.Candidate, Component: name, MaxBytes: 16384})
			assert.NotEqual(t, source.Data, read.Data, "B starts with baseline defaults, not A-authored procedure")
		}
	}
	draft = supportCopy(t, env, draft, selected.Candidate, []string{"SKILL.md"})
	child := supportMust[control.CandidateRead](t, env, control.ReadOperation, control.ReadCandidateRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, Component: "children/reviewer.md", MaxBytes: 1024})
	assert.Equal(t, supportChild, string(child.Data))
	return supportExecute(t, env, supportFreeze(t, env, draft), task)
}

// supportCopy confirms the complete advertised closure without mutating during preview.
func supportCopy(t *testing.T, env *supportEnvironment, draft control.CandidateInfo, source string, selected []string) control.CandidateInfo {
	t.Helper()
	plan := supportMust[candidate.CopyPlan](t, env, control.PreviewCopyOperation, control.PreviewCopyRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, SourceCandidate: source, Components: selected})
	current := supportMust[control.CandidateInfo](t, env, control.GetOperation, control.GetCandidateRequest{DraftID: draft.DraftID})
	assert.Equal(t, draft, current, "preview must not mutate")
	assert.Equal(t, selected, plan.Selected)
	if selected[0] == "SKILL.md" {
		require.Len(t, plan.Closure, 2)
		assert.Equal(t, []string{"SKILL.md", "helper.go"}, plan.Replace)
	}
	require.NotNil(t, plan.Replace)
	copied := supportMust[control.CopyResult](t, env, control.CopyOperation, control.CopyRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, SourceCandidate: source, Components: plan.Selected, Replace: plan.Replace, PlanDigest: plan.Digest})
	assert.Equal(t, plan.Digest, copied.PlanDigest)
	for _, origin := range copied.Origins {
		if origin.Name == selected[0] || selected[0] == "SKILL.md" && origin.Name == "helper.go" {
			assert.Equal(t, source, origin.SourceCandidate)
		}
	}
	return copied.Candidate
}

func supportEdit(t *testing.T, env *supportEnvironment, draft control.CandidateInfo, name, slot, mediaType, content string, dependencies []string) control.CandidateInfo {
	t.Helper()
	return supportMust[control.CandidateInfo](t, env, control.EditOperation, control.EditRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision, Operation: candidate.Put, Name: name, Slot: slot, MediaType: mediaType, Content: &content, Dependencies: dependencies})
}

func supportFreeze(t *testing.T, env *supportEnvironment, draft control.CandidateInfo) control.Frozen {
	t.Helper()
	request := control.RevisionRequest{DraftID: draft.DraftID, ExpectedRevision: draft.Revision}
	checked := supportMust[control.Validation](t, env, control.ValidateOperation, request)
	require.True(t, checked.Valid)
	return supportMust[control.Frozen](t, env, control.FreezeOperation, request)
}

func supportExecute(t *testing.T, env *supportEnvironment, frozen control.Frozen, task string) supportRun {
	t.Helper()
	job := supportMust[control.Job](t, env, control.SubmitOperation, control.SubmitRequest{RequestKey: fmt.Sprintf("%s-%s", frozen.Candidate, task), Candidate: frozen.Candidate, TaskID: task, Kind: eval.ExecutionProbe})
	queued := supportMust[control.Job](t, env, control.StatusOperation, control.JobRequest{JobID: job.ID})
	assert.Equal(t, control.Queued, queued.State)
	_, runErr := env.executor.RunQueued(t.Context(), "support", job.ID)
	require.NoError(t, runErr)
	status := supportMust[control.Job](t, env, control.StatusOperation, control.JobRequest{JobID: job.ID})
	record, err := env.executor.Status(t.Context(), "support", job.ID)
	require.NoError(t, err)
	storedAttempt, err := env.experiments.Load(record.ExperimentID)
	require.NoError(t, err)
	require.Len(t, storedAttempt.Attempts, 1)

	for _, storedRun := range storedAttempt.Attempts[0].Runs {
		require.Empty(t, storedRun.RunError)
	}
	require.Empty(t, storedAttempt.Attempts[0].HarnessError)
	require.Empty(t, storedAttempt.Attempts[0].Error)
	require.Equal(t, control.Succeeded, status.State)
	require.True(t, status.EvidenceAvailable)
	for _, dir := range env.directories {
		_, err := os.Stat(dir)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	prepared := storedAttempt.Attempts[0].Prepared
	require.NotNil(t, prepared)
	assert.Equal(t, supportDigest(supportJSON(env.facts[task])), prepared.SourceSnapshot)
	assert.Equal(t, supportDigest([]byte(env.facts[task].ID)), prepared.InputSnapshot)
	assert.Equal(t, frozen.Candidate, prepared.CandidateDigest)
	require.NotNil(t, prepared.Build)
	assert.Len(t, prepared.Build.ExecutableDigest, 64)
	run := supportRun{record: record, frozen: frozen, outputs: map[string][]byte{}}
	reopened, err := eval.Open(filepath.Join(env.root, "experiments"))
	require.NoError(t, err)
	reopenedCandidates, err := candidate.Open(t.Context(), filepath.Join(env.root, "candidates"), supportSurface(), supportBaseline(), validateSupportComponents)
	require.NoError(t, err)
	workspace, err := inspect.Open(filepath.Join(env.root, "inspection"), reopened, reopenedCandidates, inspect.DefaultLimits())
	require.NoError(t, err)
	view, err := workspace.View(inspect.Scope{Manifests: []inspect.ManifestRef{record.Manifest}}, inspect.ViewPolicy{Role: inspect.Editor, Name: "support.editor.v1"})
	require.NoError(t, err)
	offset := 0
	for {
		page := supportMust[inspect.Page](t, env, control.ListEvidenceOperation, control.EvidenceListRequest{JobID: job.ID, Offset: offset, Limit: 20})
		stable, err := view.List(t.Context(), inspect.ListQuery{Offset: offset, Limit: 20})
		require.NoError(t, err)
		assert.Equal(t, page, stable)
		for _, entry := range page.Entries {
			if entry.Kind != inspect.Deliverable && entry.Kind != inspect.TaskSource && entry.Kind != inspect.ComponentDiff {
				continue
			}
			body := supportMust[inspect.ReadResult](t, env, control.ReadEvidenceOperation, control.EvidenceReadRequest{JobID: job.ID, Locator: entry.Locator, MaxBytes: 16384})
			again, err := view.Read(t.Context(), inspect.ReadQuery{Locator: entry.Locator, MaxBytes: 16384})
			require.NoError(t, err)
			assert.Equal(t, body, again)
			if entry.Kind == inspect.Deliverable {
				run.outputs[entry.Label] = body.Data
				stored, err := reopened.ReadArtifact(t.Context(), record.ExperimentID, record.Attempt, entry.Label)
				require.NoError(t, err)
				assert.Equal(t, stored, body.Data)
			}
		}
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	return run
}
