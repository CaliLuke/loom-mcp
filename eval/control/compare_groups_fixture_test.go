package control_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/require"
)

// savedComparisonTrial persists an independently executed prepared trial at Repeat=1.
func savedComparisonTrial(t *testing.T, cfg control.Config, id, candidateDigest, item string, verdict eval.Verdict, configure ...func(*eval.PreparedReceipt, *control.Submission)) control.JobRecord {
	t.Helper()
	key := eval.AttemptKey{Scenario: "task", Item: item, Variant: "variant", Repeat: 1}
	receipt := eval.PreparedReceipt{
		CandidateDigest: candidateDigest, SurfaceID: "sample", SurfaceVersion: "1",
		Bindings: []eval.PreparedBinding{{ID: "files", Version: "1"}}, TaskSnapshot: "task-v1",
		SourceSnapshot: "private-source-" + candidateDigest, InputSnapshot: "private-input-" + item,
		DriverVersion: "driver-v1", ModelConfig: "private-model-" + candidateDigest,
		Environment: eval.EnvironmentIdentity{Mode: "fixture", SnapshotID: "private-environment"},
	}
	kind := eval.ExecutionEvaluation
	plan := "evaluation-v1"
	if verdict == eval.VerdictUnassessed {
		kind, plan = eval.ExecutionProbe, ""
	}
	submission := control.Submission{Scope: cfg.Scope, Candidate: candidateDigest, TaskID: "task", TaskSnapshot: "task-v1", Kind: kind, EvaluationPlan: plan, HostBinding: candidate.Binding{ID: "files", Version: "1"}}
	for _, configureTrial := range configure {
		configureTrial(&receipt, &submission)
	}
	prepare := func(context.Context, eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		return &eval.PreparedAttempt{
			Harness: eval.AttemptHarnessConfig{Register: func(context.Context, *agentsruntime.Runtime) error {
				return nil
			}},
			Drive: func(context.Context, *agenttest.Harness, eval.Item) (*agenttest.SessionResult, error) {
				return &agenttest.SessionResult{}, nil
			},
			Receipt: receipt,
			Quiesce: func(context.Context) error {
				return nil
			},
			Collect: func(context.Context, eval.CollectionInput) ([]eval.CollectedArtifact, error) {
				return nil, nil
			},
			Release: func(context.Context) error {
				return nil
			},
		}, nil
	}
	var writer *eval.Writer
	var report *eval.Report
	var err, runErr error
	if verdict == eval.VerdictUnassessed {
		pc := eval.ProbeConfig{Key: key, Item: eval.Item{ID: item, Input: "private-input"}, Variant: eval.Variant{ID: key.Variant}, PrepareAttempt: prepare, PrepareTimeout: time.Second, CleanupTimeout: time.Second}
		writer, err = cfg.Experiments.BeginProbe(t.Context(), pc, eval.ExperimentMeta{})
		require.NoError(t, err)
		pc.PreparedWriter = writer
		report, runErr = eval.Probe(t.Context(), pc)
	} else {
		ec := eval.Config{
			Scenarios: []eval.Scenario{{ID: key.Scenario, Items: eval.Items(eval.Item{ID: item, Input: "private-input"}), Assert: comparisonAssertions(verdict)}},
			Variants:  []eval.Variant{{ID: key.Variant}}, Repeats: 1,
			PrepareAttempt: prepare, PrepareTimeout: time.Second, CleanupTimeout: time.Second,
		}
		writer, err = cfg.Experiments.Begin(t.Context(), ec, eval.ExperimentMeta{})
		require.NoError(t, err)
		ec.PreparedWriter = writer
		report, runErr = eval.Run(t.Context(), ec)
	}
	require.NoError(t, runErr)
	require.NoError(t, writer.Finish(report, runErr))
	require.Len(t, report.Attempts, 1)
	require.Equal(t, verdict, report.Attempts[0].Verdict())
	return control.JobRecord{
		ID: id, State: control.Succeeded, ExperimentID: writer.ID(), Attempt: key,
		Submission: submission,
	}
}

func comparisonAssertions(verdict eval.Verdict) func(context.Context, *eval.Attempt) (eval.Assertions, error) {
	return func(context.Context, *eval.Attempt) (eval.Assertions, error) {
		switch verdict {
		case eval.VerdictError:
			return eval.Assertions{}, errors.New("private-execution-diagnostic")
		case eval.VerdictPending:
			return eval.Assertions{Claims: []eval.Claim{{ID: "private_claim", Text: "private-judge-criterion"}}}, nil
		case eval.VerdictFailed:
			return eval.Assertions{Checks: []expect.Check{{Name: "private-check", Diagnostic: "private-failure-diagnostic"}}}, nil
		case eval.VerdictPassed:
			return eval.Assertions{Checks: []expect.Check{{Name: "private-check", Passed: true}}}, nil
		case eval.VerdictUnassessed, eval.VerdictNotRun:
			return eval.Assertions{}, errors.New("unsupported assertion verdict")
		}
		return eval.Assertions{}, errors.New("invalid assertion verdict")
	}
}

func comparisonCandidate(t *testing.T, cfg control.Config) string {
	t.Helper()
	s := service(t, cfg)
	d := draft(t, s, cfg)
	content := "different candidate"
	edited, err := s.Edit(t.Context(), control.EditRequest{DraftID: d.DraftID, ExpectedRevision: d.Revision, Operation: candidate.Put, Name: "main.md", Slot: "content", MediaType: "text/plain", Content: &content})
	require.NoError(t, err)
	frozen, err := s.Freeze(t.Context(), control.RevisionRequest{DraftID: edited.DraftID, ExpectedRevision: edited.Revision})
	require.NoError(t, err)
	require.NotEqual(t, cfg.Candidates.Discover().BaselineDigest, frozen.Candidate)
	return frozen.Candidate
}

// comparisonStoreBytes snapshots every persisted byte to detect comparison writes.
func comparisonStoreBytes(t *testing.T, root string) map[string][]byte {
	t.Helper()
	dir, err := os.OpenRoot(root)
	require.NoError(t, err)
	files := make(map[string][]byte)
	err = fs.WalkDir(dir.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := dir.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = data
		return nil
	})
	require.NoError(t, errors.Join(err, dir.Close()))
	return files
}

// comparisonGroup locates a public group by an authorized job identity.
func comparisonGroup(t *testing.T, comparison control.Comparison, jobID string) control.ComparisonGroup {
	t.Helper()
	for _, group := range comparison.Groups {
		for _, job := range group.BaselineJobs {
			if job.JobID == jobID {
				return group
			}
		}
	}
	t.Fatalf("missing comparison group for job %q", jobID)
	return control.ComparisonGroup{}
}
