package agentfeatures_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"example.com/agentfeatures/gen/features/agents/specialist"
	delegatedtools "example.com/agentfeatures/gen/features/agents/specialist/agenttools/delegated"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// supportReadFile confines collection to the acquired host directory.
func supportReadFile(dir, name string) (data []byte, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, root.Close())
	}()
	return root.ReadFile(name)
}

// prepareSupportAttempt validates the complete installation contract before acquiring resources.
func (env *supportEnvironment) prepareSupportAttempt(ctx context.Context, frozen *candidate.Frozen, facts supportFacts, info eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
	if err := env.validateSupportSurface(ctx, frozen); err != nil {
		return nil, err
	}
	child, err := frozen.Read("children/reviewer.md")
	if err != nil {
		return nil, err
	}
	if _, err := supportSection(string(child)); err != nil {
		return nil, err
	}
	source, err := frozen.Read("helper.go")
	if err != nil {
		return nil, err
	}
	skill, err := frozen.Read("SKILL.md")
	if err != nil {
		return nil, err
	}
	system, err := frozen.Read("system.md")
	if err != nil {
		return nil, err
	}
	guidance, err := frozen.Read("guidance.md")
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "loom-support-")
	if err != nil {
		return nil, err
	}
	env.directories = append(env.directories, dir)
	name := "document.md"
	if facts.Kind == "graph" {
		name = "release.md"
	}
	declarations := []eval.Deliverable{{Name: name, MediaType: "text/markdown"}, {Name: "receipt.json", MediaType: "application/json"}}
	lease := &eval.PreparedAttempt{Release: func(context.Context) error {
		return os.RemoveAll(dir)
	}, Quiesce: func(ctx context.Context) error {
		return ctx.Err()
	}, Deliverables: declarations, Collect: func(ctx context.Context, in eval.CollectionInput) ([]eval.CollectedArtifact, error) {
		if err := errors.Join(ctx.Err(), in.EvidenceError); err != nil {
			return nil, err
		}
		source, err := supportReadFile(dir, "source.json")
		if err != nil {
			return nil, err
		}
		if string(source) != string(supportJSON(facts)) {
			return nil, errors.New("read-only task source changed")
		}
		var artifacts []eval.CollectedArtifact
		for _, declaration := range declarations {
			data, err := supportReadFile(dir, declaration.Name)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, eval.CollectedArtifact{Name: declaration.Name, MediaType: declaration.MediaType, Data: data})
		}
		return artifacts, nil
	}}
	bindings, err := testfixture.InstallComponentBindings(ctx, dir, source, skill)
	if err != nil {
		return lease, err
	}
	if err := os.WriteFile(filepath.Join(dir, "source.json"), supportJSON(facts), 0o600); err != nil {
		return lease, err
	}
	if err := os.WriteFile(filepath.Join(dir, "unopened.txt"), []byte("UNOPENED-PHYSICAL-SOURCE"), 0o600); err != nil {
		return lease, err
	}
	if facts.Kind == "document" {
		if err := os.WriteFile(filepath.Join(dir, "document.md"), []byte(facts.Body), 0o600); err != nil {
			return lease, err
		}
	}
	domain := &supportDomain{dir: dir, facts: facts}
	domainRegistration := domain.registration(string(guidance))
	solver, err := model.NewClient(&supportSolver{kind: facts.Kind, id: facts.ID, calls: &env.calls})
	if err != nil {
		return lease, err
	}
	childClient, err := model.NewClient(supportChildModel{})
	if err != nil {
		return lease, err
	}
	manifest := frozen.Manifest()
	lease.Receipt = eval.PreparedReceipt{CandidateDigest: frozen.Digest(), SurfaceID: manifest.SurfaceID, SurfaceVersion: manifest.SurfaceVersion, TaskSnapshot: supportDigest(supportJSON(facts)), SourceSnapshot: supportDigest(supportJSON(facts)), InputSnapshot: supportDigest([]byte(info.Item.Input)), ModelConfig: "support.deterministic.v1", Environment: eval.EnvironmentIdentity{Mode: "fixture", SnapshotID: supportDigest(supportJSON(facts))}, Build: bindings.Build}
	for _, binding := range manifest.Bindings {
		lease.Receipt.Bindings = append(lease.Receipt.Bindings, eval.PreparedBinding{ID: binding.ID, Version: binding.Version})
	}
	lease.Inputs = []agenttest.Input{{&model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: string(system)}}}, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: info.Item.Input}}}}}
	lease.BaseOptions = []agenttest.Option{agenttest.PassthroughAll()}
	lease.Harness.Register = func(ctx context.Context, rt *agentsruntime.Runtime) error {
		if err := rt.RegisterModel("support-solver", solver); err != nil {
			return err
		}
		if err := rt.RegisterModel("support-child", childClient); err != nil {
			return err
		}
		var specs []tools.ToolSpec
		for _, registration := range bindings.Toolsets {
			if err := rt.RegisterToolset(registration); err != nil {
				return err
			}
			specs = append(specs, registration.Specs...)
		}
		if err := rt.RegisterToolset(domainRegistration); err != nil {
			return err
		}
		specs = append(specs, domainRegistration.Specs...)
		// The host grants this child one current-source read; authored text cannot expand it.
		readSpec := domainRegistration.Specs[0]
		if facts.Kind == "graph" {
			readSpec = domainRegistration.Specs[1]
		}
		childAgent, err := specialist.NewSpecialistAgent(specialist.SpecialistAgentConfig{Planner: supportPlanner{name: "support-child", child: true, specs: []tools.ToolSpec{readSpec}}})
		if err != nil {
			return err
		}
		if err := rt.RegisterAgent(ctx, agentsruntime.AgentRegistration{ID: specialist.AgentID, Planner: childAgent.Planner, Specs: []tools.ToolSpec{readSpec}, Workflow: engine.WorkflowDefinition{Name: specialist.WorkflowName, TaskQueue: specialist.DefaultTaskQueue, Handler: rt.ExecuteWorkflow}, PlanActivityName: specialist.PlanActivity, PlanActivityOptions: engine.ActivityOptions{Queue: specialist.DefaultTaskQueue}, ResumeActivityName: specialist.ResumeActivity, ResumeActivityOptions: engine.ActivityOptions{Queue: specialist.DefaultTaskQueue}, ExecuteToolActivity: specialist.ExecuteToolActivity, Policy: agentsruntime.RunPolicy{MaxToolCalls: 1, MaxRecoveryTurns: 1, TimeBudget: 10 * time.Second, InterruptsAllowed: true}}); err != nil {
			return err
		}
		registration, err := delegatedtools.NewRegistration(rt, string(child))
		if err != nil {
			return err
		}
		if err := rt.RegisterToolset(registration); err != nil {
			return err
		}
		specs = append(specs, registration.Specs...)
		return rt.RegisterAgent(ctx, agentsruntime.AgentRegistration{ID: "support.solver", Planner: supportPlanner{name: "support-solver", specs: specs}, Specs: specs, Workflow: engine.WorkflowDefinition{Name: "support.solver.workflow", TaskQueue: "support.solver", Handler: rt.ExecuteWorkflow}, PlanActivityName: "support.solver.plan", ResumeActivityName: "support.solver.resume", ExecuteToolActivity: "support.solver.tool"})
	}
	return lease, nil
}

func (env *supportEnvironment) validateSupportSurface(ctx context.Context, frozen *candidate.Frozen) error {
	baseline, err := env.candidates.Load(ctx, env.candidates.Discover().BaselineDigest)
	if err != nil {
		return err
	}
	actual := frozen.Manifest()
	expected := baseline.Manifest()
	if actual.SurfaceDigest != expected.SurfaceDigest || actual.SurfaceID != expected.SurfaceID || actual.SurfaceVersion != expected.SurfaceVersion || !reflect.DeepEqual(actual.Bindings, expected.Bindings) {
		return errors.New("unsupported consumer surface, binding or activation")
	}
	return nil
}
