package agentfeatures_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	controlfixture "github.com/CaliLuke/loom-mcp/v2/eval/control/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

func newSupportEnvironment(t *testing.T) (*supportEnvironment, error) {
	root := t.TempDir()
	candidates, err := candidate.Open(t.Context(), filepath.Join(root, "candidates"), supportSurface(), supportBaseline(), validateSupportComponents)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "candidates"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), "draft-"))
	}
	experiments, err := eval.Open(filepath.Join(root, "experiments"))
	if err != nil {
		return nil, err
	}
	workspace, err := inspect.Open(filepath.Join(root, "inspection"), experiments, candidates, inspect.DefaultLimits())
	if err != nil {
		return nil, err
	}
	env := &supportEnvironment{root: root, candidates: candidates, experiments: experiments, workspace: workspace, facts: supportTaskFacts()}
	executor, err := controlfixture.OpenExecutor(controlfixture.ExecutorConfig{Dir: filepath.Join(root, "executor"), Run: env.runSupportTrial})
	if err != nil {
		return nil, err
	}
	env.executor = executor
	t.Cleanup(func() {
		require.NoError(t, executor.Close())
	})
	var tasks []control.TaskBinding
	for id, facts := range env.facts {
		var bindings []candidate.Binding
		for _, slot := range supportSurface().Slots {
			bindings = append(bindings, slot.Binding)
		}
		tasks = append(tasks, control.TaskBinding{Task: control.Task{ID: id, Modes: []eval.ExecutionKind{eval.ExecutionProbe}}, Snapshot: supportDigest(supportJSON(facts)), HostBinding: candidate.Binding{ID: "consumer-host", Version: "1"}, ComponentBindings: bindings})
	}
	operations := append(experimentControllerOperations(), control.PreviewCopyOperation, control.CopyOperation)
	service, err := control.New(control.Config{Scope: "support", Role: inspect.Editor, Candidates: candidates, Experiments: experiments, Tasks: tasks, Executor: executor, Operations: operations, Authorize: func(context.Context, control.Operation) error {
		return nil
	}, Limits: control.DefaultLimits(), Evidence: func(ctx context.Context, id string) (*inspect.View, error) {
		record, err := executor.Status(ctx, "support", id)
		if err != nil {
			return nil, err
		}
		if record.Manifest == "" {
			return nil, control.NewError(control.EvidenceNotReady, "", "", "")
		}
		return workspace.View(inspect.Scope{Manifests: []inspect.ManifestRef{record.Manifest}}, inspect.ViewPolicy{Role: inspect.Editor, Name: "support.editor.v1"})
	}})
	if err != nil {
		return nil, err
	}
	registration, err := control.NewToolsetRegistration(control.ToolsetConfig{Service: service})
	if err != nil {
		return nil, err
	}
	client, err := model.NewClient(supportControllerModel{})
	if err != nil {
		return nil, err
	}
	env.controller, err = agenttest.NewHarness(t.Context(), func(ctx context.Context, rt *agentsruntime.Runtime) error {
		if err := rt.RegisterModel("controller", client); err != nil {
			return err
		}
		if err := rt.RegisterToolset(registration); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, agentsruntime.AgentRegistration{ID: "support.controller", Planner: experimentControllerPlanner{specs: registration.Specs}, Specs: registration.Specs, Workflow: engine.WorkflowDefinition{Name: "support.controller.workflow", TaskQueue: "support.controller", Handler: rt.ExecuteWorkflow}, PlanActivityName: "support.controller.plan", ResumeActivityName: "support.controller.resume", ExecuteToolActivity: "support.controller.tool"})
	}, agenttest.PassthroughAll())
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, env.controller.Close())
	})
	return env, nil
}

func supportDigest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// supportJSON encodes fixture-owned finite data; a failure is a programming error.
func supportJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

// runSupportTrial is explicit host orchestration; native submission never starts it.
func (env *supportEnvironment) runSupportTrial(ctx context.Context, submission control.Submission) (controlfixture.Result, error) {
	facts, ok := env.facts[submission.TaskID]
	if !ok || submission.TaskSnapshot != supportDigest(supportJSON(facts)) || submission.HostBinding != (candidate.Binding{ID: "consumer-host", Version: "1"}) || submission.Kind != eval.ExecutionProbe {
		return controlfixture.Result{Stopped: true}, errors.New("unavailable task binding")
	}
	frozen, err := env.candidates.Load(ctx, submission.Candidate)
	if err != nil {
		return controlfixture.Result{Stopped: true}, err
	}
	cfg := eval.ProbeConfig{Key: eval.AttemptKey{Variant: "candidate", Scenario: submission.TaskID, Item: "current", Repeat: 1}, Item: eval.Item{ID: "current", Input: facts.ID, Task: "Read current sources, apply the skill and child review, and retain the actual result."}, Variant: eval.Variant{ID: "candidate"}, Agent: "support.solver", Timeout: 30 * time.Second, PrepareTimeout: 30 * time.Second, CleanupTimeout: 5 * time.Second}
	var declarations []eval.Deliverable
	cfg.PrepareAttempt = func(ctx context.Context, info eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		lease, err := env.prepareSupportAttempt(ctx, frozen, facts, info)
		if err == nil {
			lease.Receipt.TaskSnapshot = submission.TaskSnapshot
			declarations = append([]eval.Deliverable(nil), lease.Deliverables...)
		}
		return lease, err
	}
	writer, err := env.experiments.BeginProbe(ctx, cfg, eval.ExperimentMeta{Label: "consumer integration proof"})
	if err != nil {
		return controlfixture.Result{Stopped: true}, err
	}
	cfg.PreparedWriter = writer
	report, runErr := eval.Probe(ctx, cfg)
	finishErr := writer.Finish(report, runErr)
	result := controlfixture.Result{Stopped: true, ExperimentID: writer.ID(), Attempt: cfg.Key}
	if report == nil {
		return result, errors.Join(runErr, finishErr)
	}
	if err := errors.Join(runErr, finishErr, report.Attempts[0].HarnessErr); err != nil {
		return result, err
	}
	ref, err := env.workspace.Publish(ctx, inspect.Publication{Origin: inspect.Origin{ExperimentID: writer.ID(), Key: cfg.Key}, BaselineDigest: frozen.Manifest().BaselineDigest, CandidateDigest: frozen.Digest(), ExpectedDeliverables: declarations, Attachments: []inspect.Attachment{{Name: "current-source.json", Kind: inspect.TaskSource, MediaType: "application/json", Data: supportJSON(facts), Audiences: []inspect.Role{inspect.Editor}, Provenance: "fresh task source"}, {Name: "unopened.txt", Kind: inspect.TaskSource, MediaType: "text/plain", Data: []byte("UNOPENED-PHYSICAL-SOURCE"), Audiences: []inspect.Role{inspect.Editor}}}, Policies: []inspect.Policy{{Role: inspect.Editor, Name: "support.editor.v1", RunContent: func(content runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return bytes.Clone(content.Value), true, nil
	}}}})
	if err != nil {
		return result, err
	}
	result.Manifest = ref
	result.Finalized = true
	return result, nil
}
