// Package testfixture provides executable application hosts for eval contract tests.
package testfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// ComponentHost installs a frozen skill and compiles its submitted Go helper.
// It is an application fixture, not a sandbox for untrusted source.
type ComponentHost struct {
	mu        sync.Mutex
	frozen    *candidate.Frozen
	directory string
	calls     int
}

type componentSolver struct {
	host   *ComponentHost
	prefix *agenttest.Script
}

type componentPlanner struct{ specs []tools.ToolSpec }

// NewComponentHost binds one immutable candidate to its application host.
func NewComponentHost(frozen *candidate.Frozen) *ComponentHost {
	return &ComponentHost{frozen: frozen}
}

// Prepare acquires the isolated executable environment for one attempt.
func (h *ComponentHost) Prepare(ctx context.Context, info eval.PreparedAttemptInfo) (*eval.PreparedAttempt, error) {
	if err := validateComponentBindings(h.frozen.Manifest().Bindings); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "loom-component-")
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.directory = dir
	h.mu.Unlock()
	lease := componentLease(dir)
	source, err := h.frozen.Read("helper.go")
	if err != nil {
		return lease, err
	}
	skill, err := h.frozen.Read("SKILL.md")
	if err != nil {
		return lease, err
	}
	bindings, err := InstallComponentBindings(ctx, dir, source, skill)
	if err != nil {
		return lease, err
	}
	receipt := componentReceipt(h.frozen, info.Item, source, bindings.Build)
	provider, err := newComponentSolver(h, info.Item.Input)
	if err != nil {
		return lease, err
	}
	client, err := model.NewClient(provider)
	if err != nil {
		return lease, err
	}
	lease.Receipt = receipt
	lease.Inputs = []agenttest.Input{agenttest.User(info.Item.Input)}
	lease.BaseOptions = []agenttest.Option{agenttest.PassthroughAll()}
	lease.Harness.Register = func(ctx context.Context, rt *runtime.Runtime) error {
		return registerComponent(ctx, rt, bindings, client)
	}
	return lease, nil
}

// Directory returns the environment path, including after it has been released.
func (h *ComponentHost) Directory() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.directory
}

// ModelCalls reports requests dispatched to the deterministic solver provider.
func (h *ComponentHost) ModelCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// PlanStart forwards the initial canonical messages to the solver model.
func (p componentPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages)
}

// PlanResume forwards tool results through the subsequent canonical request.
func (p componentPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages)
}

// Complete scripts skill activation, then computes its response from the real tool result.
func (s *componentSolver) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	s.host.mu.Lock()
	s.host.calls++
	s.host.mu.Unlock()
	if s.prefix.Remaining() > 0 {
		return s.prefix.Client().Complete(ctx, request)
	}
	output, err := helperStdout(request)
	if err != nil {
		return nil, err
	}
	final, err := agenttest.NewScript(agenttest.Respond(agenttest.Text("Computed: " + output + "\n")))
	if err != nil {
		return nil, err
	}
	return final.Client().Complete(ctx, request)
}

// Stream is unavailable because the fixture planner uses complete requests.
func (s *componentSolver) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, errors.New("component: streaming is not used")
}

// validateComponentBindings admits only this fixture's advertised installation contracts.
func validateComponentBindings(bindings []candidate.Binding) error {
	supported := map[string]string{"skill-files": "1", "go-helper": "1"}
	if len(bindings) != len(supported) {
		return errors.New("unsupported component binding set")
	}
	for _, binding := range bindings {
		if supported[binding.ID] != binding.Version {
			return fmt.Errorf("unsupported component binding %s@%s", binding.ID, binding.Version)
		}
	}
	return nil
}

func componentReceipt(frozen *candidate.Frozen, item eval.Item, source []byte, build *eval.PreparedBuild) eval.PreparedReceipt {
	manifest := frozen.Manifest()
	receipt := eval.PreparedReceipt{CandidateDigest: frozen.Digest(), SurfaceID: manifest.SurfaceID, SurfaceVersion: manifest.SurfaceVersion, TaskSnapshot: componentDigest([]byte(item.Task)), SourceSnapshot: componentDigest(source), InputSnapshot: componentDigest([]byte(item.Input)), ModelConfig: "deterministic-component-solver-v1", Environment: eval.EnvironmentIdentity{Mode: "fixture", SnapshotID: frozen.Digest()}, Build: build}
	for _, binding := range manifest.Bindings {
		receipt.Bindings = append(receipt.Bindings, eval.PreparedBinding{ID: binding.ID, Version: binding.Version})
	}
	return receipt
}

func registerComponent(ctx context.Context, rt *runtime.Runtime, bindings ComponentBindings, client model.Client) error {
	if err := rt.RegisterModel("component", client); err != nil {
		return err
	}
	for _, registration := range bindings.Toolsets {
		if err := rt.RegisterToolset(registration); err != nil {
			return err
		}
	}
	specs := append([]tools.ToolSpec{}, bindings.Toolsets[1].Specs...)
	specs = append(specs, bindings.Toolsets[0].Specs...)
	return rt.RegisterAgent(ctx, runtime.AgentRegistration{ID: "component.solver", Planner: componentPlanner{specs: specs}, Specs: specs, Workflow: engine.WorkflowDefinition{Name: "component.workflow", TaskQueue: "component", Handler: rt.ExecuteWorkflow}, PlanActivityName: "component.plan", ResumeActivityName: "component.resume", ExecuteToolActivity: "component.tool"})
}

func newComponentSolver(host *ComponentHost, input string) (*componentSolver, error) {
	payload, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return nil, err
	}
	script, err := agenttest.NewScript(
		agenttest.Respond(agenttest.RawToolCall("skills.list_skills", rawjson.Message(`{}`))),
		agenttest.Respond(agenttest.RawToolCall("skills.load_skill", rawjson.Message(`{"skill":"transform"}`))).When(agenttest.LastToolResult(tools.Ident("skills.list_skills"), "transform")),
		agenttest.Respond(agenttest.RawToolCall("components.run_helper", payload)).When(agenttest.LastToolResult(tools.Ident("skills.load_skill"), "components.run_helper")),
	)
	if err != nil {
		return nil, err
	}
	return &componentSolver{host: host, prefix: script}, nil
}

// collectComponentAnswer materializes only the final text in the closed canonical capture.
func collectComponentAnswer(ctx context.Context, dir string, in eval.CollectionInput) ([]eval.CollectedArtifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.EvidenceError != nil {
		return nil, in.EvidenceError
	}
	if in.Evidence == nil || len(in.Evidence.Records) != 1 || len(in.Evidence.Records[0].Runs) != 1 {
		return nil, errors.New("component: missing canonical run")
	}
	transcript := in.Evidence.Records[0].Runs[0].Transcript
	if len(transcript) == 0 {
		return nil, errors.New("component: missing final answer")
	}
	final := transcript[len(transcript)-1]
	if final.Role != model.ConversationRoleAssistant {
		return nil, errors.New("component: missing final assistant answer")
	}
	var text strings.Builder
	for _, part := range final.Parts {
		if value, ok := part.(model.TextPart); ok {
			text.WriteString(value.Text)
		}
	}
	if text.Len() == 0 {
		return nil, errors.New("component: empty final answer")
	}
	path := filepath.Join(dir, "answer.txt")
	if err := os.WriteFile(path, []byte(text.String()), 0o600); err != nil {
		return nil, err
	}
	data, err := readComponentFile(dir, "answer.txt")
	if err != nil {
		return nil, err
	}
	return []eval.CollectedArtifact{{Name: "answer.txt", MediaType: "text/plain", Data: data}}, nil
}

func componentDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// helperStdout resolves the actual helper result through its matching tool-use ID.
func helperStdout(request *model.Request) (string, error) {
	if err := agenttest.LastToolResult(tools.Ident("components.run_helper"), "")(request); err != nil {
		return "", err
	}
	calls := map[string]bool{}
	var content any
	for _, message := range request.Messages {
		for _, part := range message.Parts {
			switch value := part.(type) {
			case model.ToolUsePart:
				calls[value.ID] = value.Name == "components.run_helper"
			case model.ToolResultPart:
				if calls[value.ToolUseID] {
					content = value.Content
				}
			}
		}
	}
	data, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	var output struct {
		Stdout *string `json:"stdout"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return "", err
	}
	if output.Stdout == nil {
		return "", errors.New("component: helper result has no stdout")
	}
	return *output.Stdout, nil
}

// componentLease binds cleanup and collection to one acquired environment.
func componentLease(dir string) *eval.PreparedAttempt {
	return &eval.PreparedAttempt{
		Release: func(context.Context) error {
			return os.RemoveAll(dir)
		},
		Quiesce: func(ctx context.Context) error {
			return ctx.Err()
		},
		Collect: func(ctx context.Context, in eval.CollectionInput) ([]eval.CollectedArtifact, error) {
			return collectComponentAnswer(ctx, dir, in)
		},
		Deliverables: []eval.Deliverable{{Name: "answer.txt", MediaType: "text/plain"}},
	}
}

func (p componentPlanner) complete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("component")
	if !ok {
		return nil, errors.New("component: model is not registered")
	}
	definitions := make([]*model.ToolDefinition, 0, len(p.specs))
	for _, spec := range p.specs {
		definitions = append(definitions, &model.ToolDefinition{Name: spec.Name.String(), Description: spec.Description, InputSchema: map[string]any{"type": "object"}})
	}
	response, err := client.Complete(ctx, &model.Request{Model: "deterministic", Messages: messages, Tools: definitions})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{}
	for _, call := range response.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: call.Name, ToolCallID: call.ID, Payload: call.Payload})
	}
	if len(result.ToolCalls) == 0 {
		result.FinalResponse = &planner.FinalResponse{Message: &response.Content[0]}
	}
	return result, nil
}
