package agentfeatures_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"go/parser"
	"go/token"
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
	"github.com/CaliLuke/loom-mcp/v2/eval/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

const experimentSkill = "---\nid: transform\nname: Transform\ndescription: Transform the task word.\n---\nUse components.run_helper with the task input.\n"

// newExperimentControllerFixture installs only an authorized baseline and host task registry.
func newExperimentControllerFixture(t *testing.T) (*experimentControllerFixture, error) {
	t.Helper()
	root := t.TempDir()
	surface := experimentControlSurface()
	candidates, err := candidate.Open(t.Context(), filepath.Join(root, "candidates"), surface, []candidate.Component{
		{Name: "SKILL.md", Slot: "skills", MediaType: "text/markdown", Data: []byte(experimentSkill), Dependencies: []string{"helper.go"}},
		{Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Data: []byte("package main\n\nfunc main() {\n}\n")},
	}, validateControllerSource)
	if err != nil {
		return nil, err
	}
	initial, err := os.ReadDir(filepath.Join(root, "candidates"))
	if err != nil {
		return nil, err
	}
	for _, entry := range initial {
		assert.False(t, strings.HasPrefix(entry.Name(), "draft-"), "controller must create its own draft")
	}
	experiments, err := eval.Open(filepath.Join(root, "experiments"))
	if err != nil {
		return nil, err
	}
	workspace, err := inspect.Open(filepath.Join(root, "inspection"), experiments, candidates, inspect.DefaultLimits())
	if err != nil {
		return nil, err
	}
	fixture := &experimentControllerFixture{firstDone: make(chan struct{}), secondStarted: make(chan struct{}), secondDone: make(chan struct{}), started: make(chan struct{}), allowFinalization: make(chan struct{}), candidates: candidates, experiments: experiments, workspace: workspace}
	fixture.controller = &experimentController{firstJob: make(chan string, 1), secondJob: make(chan string, 1), cancelPolled: make(chan struct{}), fixture: fixture}
	executor, err := controlfixture.OpenExecutor(controlfixture.ExecutorConfig{Dir: filepath.Join(root, "executor"), Run: fixture.runTrial})
	if err != nil {
		return nil, err
	}
	fixture.executor = executor
	t.Cleanup(func() {
		require.NoError(t, executor.Close())
	})
	binding := candidate.Binding{ID: "component-host", Version: "1"}
	tasks := make([]control.TaskBinding, 0, 2)
	for _, id := range []string{"transform", "cooperative"} {
		tasks = append(tasks, control.TaskBinding{Task: control.Task{ID: id, Modes: []eval.ExecutionKind{eval.ExecutionProbe}}, Snapshot: id + "-v1", HostBinding: binding, ComponentBindings: []candidate.Binding{surface.Slots[0].Binding, surface.Slots[1].Binding}})
	}
	service, err := control.New(control.Config{Scope: "controller", Role: inspect.Editor, Candidates: candidates, Experiments: experiments, Tasks: tasks, Executor: executor, Evidence: func(ctx context.Context, id string) (*inspect.View, error) {
		record, err := executor.Status(ctx, "controller", id)
		if err != nil {
			return nil, err
		}
		if record.Manifest == "" {
			return nil, control.NewError(control.EvidenceNotReady, "", "", "")
		}
		return workspace.View(inspect.Scope{Manifests: []inspect.ManifestRef{record.Manifest}}, inspect.ViewPolicy{Role: inspect.Editor, Name: "controller.editor.v1"})
	}, Operations: experimentControllerOperations(), Authorize: func(context.Context, control.Operation) error {
		return nil
	}, Limits: control.DefaultLimits()})
	if err != nil {
		return nil, err
	}
	registration, err := control.NewToolsetRegistration(control.ToolsetConfig{Service: service})
	if err != nil {
		return nil, err
	}
	client, err := model.NewClient(fixture.controller)
	if err != nil {
		return nil, err
	}
	harness, err := agenttest.NewHarness(t.Context(), func(ctx context.Context, rt *agentsruntime.Runtime) error {
		if err := rt.RegisterModel("controller", client); err != nil {
			return err
		}
		if err := rt.RegisterToolset(registration); err != nil {
			return err
		}
		return rt.RegisterAgent(ctx, agentsruntime.AgentRegistration{ID: "experiment.controller", Planner: experimentControllerPlanner{specs: registration.Specs}, Specs: registration.Specs, Workflow: engine.WorkflowDefinition{Name: "controller.workflow", TaskQueue: "controller", Handler: rt.ExecuteWorkflow}, PlanActivityName: "controller.plan", ResumeActivityName: "controller.resume", ExecuteToolActivity: "controller.tool"})
	}, agenttest.PassthroughAll())
	if err != nil {
		return nil, err
	}
	fixture.harness = harness
	t.Cleanup(func() {
		require.NoError(t, harness.Close())
	})
	return fixture, nil
}

func experimentControlSurface() candidate.Surface {
	return candidate.Surface{ID: "native-controller", Version: "1", MaxTotalBytes: 32768, Slots: []candidate.Slot{
		{Name: "skills", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/markdown"}, MaxBytes: 8192, MaxComponents: 1, Binding: candidate.Binding{ID: "skill-files", Version: "1"}},
		{Name: "helpers", Activation: candidate.OnDemand, Operations: []candidate.Operation{candidate.Put}, MediaTypes: []string{"text/x-go"}, MaxBytes: 16384, MaxComponents: 2, Binding: candidate.Binding{ID: "go-helper", Version: "1"}},
	}}
}

func experimentControllerOperations() []control.Operation {
	return []control.Operation{control.DiscoverOperation, control.CreateOperation, control.GetOperation, control.ReadOperation, control.EditOperation, control.ValidateOperation, control.FreezeOperation, control.SubmitOperation, control.StatusOperation, control.CancelOperation, control.ListEvidenceOperation, control.ReadEvidenceOperation, control.CompareOperation}
}

// validateControllerSource rejects invalid Go before a draft revision changes.
func validateControllerSource(_ context.Context, components []candidate.Component) error {
	for _, component := range components {
		if component.MediaType == "text/x-go" {
			if _, err := parser.ParseFile(token.NewFileSet(), component.Name, component.Data, parser.AllErrors); err != nil {
				return err
			}
		}
	}
	return nil
}

// onlyExperimentCancellation rejects joined cleanup failures rather than masking them as cancellation.
func onlyExperimentCancellation(err error) bool {
	if err == nil {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range many.Unwrap() {
			if !onlyExperimentCancellation(cause) {
				return false
			}
		}
		return true
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return onlyExperimentCancellation(one.Unwrap())
	}
	return errors.Is(err, context.Canceled)
}

// runTrial is explicit host orchestration, called only by the test's RunQueued calls.
func (f *experimentControllerFixture) runTrial(ctx context.Context, submission control.Submission) (controlfixture.Result, error) {
	if submission.HostBinding != (candidate.Binding{ID: "component-host", Version: "1"}) || submission.TaskSnapshot != submission.TaskID+"-v1" || (submission.TaskID != "transform" && submission.TaskID != "cooperative") || submission.Kind != eval.ExecutionProbe || submission.EvaluationPlan != "" {
		return controlfixture.Result{Stopped: true}, errors.New("recorded host binding unavailable")
	}
	frozen, err := f.candidates.Load(ctx, submission.Candidate)
	if err != nil {
		return controlfixture.Result{Stopped: true}, err
	}
	host := testfixture.NewComponentHost(frozen)
	cfg := eval.ProbeConfig{Key: eval.AttemptKey{Variant: "candidate", Scenario: submission.TaskID, Item: "one", Repeat: 1}, Item: eval.Item{ID: "one", Input: "Lóom", Task: "Use the installed skill."}, Variant: eval.Variant{ID: "candidate"}, Agent: "component.solver", Timeout: 30 * time.Second, PrepareTimeout: 30 * time.Second, CleanupTimeout: 5 * time.Second}
	var declarations []eval.Deliverable
	cfg.PrepareAttempt = func(ctx context.Context, info eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
		lease, err := host.Prepare(ctx, info)
		if host.Directory() != "" {
			f.directories = append(f.directories, host.Directory())
		}
		if err != nil {
			return lease, err
		}
		lease.Receipt.TaskSnapshot = submission.TaskSnapshot
		if submission.TaskID == "cooperative" {
			lease.Inputs = nil
			lease.Receipt.DriverVersion = "cooperative.driver.v1"
			lease.Deliverables = nil
			lease.Collect = func(context.Context, eval.CollectionInput) ([]eval.CollectedArtifact, error) {
				return nil, nil
			}
			lease.Drive = func(ctx context.Context, _ *agenttest.Harness, _ eval.Item) (*agenttest.SessionResult, error) {
				close(f.started)
				<-ctx.Done()
				<-f.allowFinalization
				return nil, ctx.Err()
			}
		}
		declarations = append([]eval.Deliverable(nil), lease.Deliverables...)
		return lease, nil
	}
	writer, err := f.experiments.BeginProbe(ctx, cfg, eval.ExperimentMeta{Label: "native controller trial"})
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
	executionErr := errors.Join(runErr, report.Attempts[0].HarnessErr)
	canceled := submission.TaskID == "cooperative" && onlyExperimentCancellation(executionErr)
	if !canceled && executionErr != nil {
		return result, errors.Join(executionErr, finishErr)
	}
	if finishErr != nil {
		return result, finishErr
	}
	ref, err := f.workspace.Publish(context.WithoutCancel(ctx), inspect.Publication{Origin: inspect.Origin{ExperimentID: writer.ID(), Key: cfg.Key}, CandidateDigest: submission.Candidate, ExpectedDeliverables: declarations, Policies: []inspect.Policy{{Role: inspect.Editor, Name: "controller.editor.v1", RunContent: func(content runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		return bytes.Clone(content.Value), true, nil
	}}}})
	if err != nil {
		return result, err
	}
	result.Manifest = ref
	result.Finalized = true
	result.Canceled = canceled
	return result, nil
}
