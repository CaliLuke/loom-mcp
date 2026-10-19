package agentfeatures_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type experimentControllerPlanner struct{ specs []tools.ToolSpec }

const controllerHelperSource = `package main

import (
 "fmt"
 "io"
 "os"
 "strings"
)

func main() {
 input, err := io.ReadAll(os.Stdin)
 if err != nil {
  fmt.Fprintln(os.Stderr, err)
  os.Exit(1)
 }
 fmt.Print("new:" + strings.ToUpper(string(input)))
}
`

// Complete authors candidate edits using only advertised tools and prior tool results.
func (c *experimentController) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	operation, payload, err := c.next(ctx, request)
	if err != nil {
		return nil, err
	}
	var turn agenttest.Turn
	if operation == "" {
		turn = agenttest.Respond(agenttest.Text("Controller inspected: " + string(c.inspected) + "Second job: canceled; quality: " + c.comparison.Status))
	} else {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		turn = agenttest.Respond(agenttest.RawToolCall(tools.Ident("experiment."+string(operation)), rawjson.Message(raw)), agenttest.CallID(fmt.Sprintf("controller-%d", c.step)))
	}
	script, err := agenttest.NewScript(turn)
	if err != nil {
		return nil, err
	}
	c.step++
	return script.Client().Complete(ctx, request)
}

// Stream is unused because the controller planner uses complete requests.
func (*experimentController) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, errors.New("controller uses complete requests")
}

// PlanStart forwards the initial request with actual closed native tool schemas.
func (p experimentControllerPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	return p.complete(ctx, input.Agent, input.Messages)
}

// PlanResume preserves actual tool results for the controller's next decision.
func (p experimentControllerPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, input.Agent, input.Messages)
}

func decodeExperimentResult[T any](request *model.Request) (T, error) {
	var output T
	part, err := latestExperimentResult(request)
	if err != nil {
		return output, err
	}
	if part.IsError {
		return output, fmt.Errorf("unexpected control failure: %v", part.Content)
	}
	data, err := json.Marshal(part.Content)
	if err != nil {
		return output, err
	}
	err = json.Unmarshal(data, &output)
	return output, err
}

func latestExperimentResult(request *model.Request) (model.ToolResultPart, error) {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		message := request.Messages[i]
		for j := len(message.Parts) - 1; j >= 0; j-- {
			if part, ok := message.Parts[j].(model.ToolResultPart); ok {
				return part, nil
			}
		}
	}
	return model.ToolResultPart{}, errors.New("controller has no tool result")
}

func checkControllerSchemas(request *model.Request) error {
	for _, operation := range experimentControllerOperations() {
		found := false
		for _, definition := range request.Tools {
			if definition.Name != "experiment."+string(operation) {
				continue
			}
			found = true
			encoded, err := json.Marshal(definition.InputSchema)
			if err != nil {
				return err
			}
			if !bytes.Contains(encoded, []byte(`"additionalProperties":false`)) {
				return errors.New("controller requires closed schemas")
			}
			if operation == control.EditOperation && !bytes.Contains(encoded, []byte(`"content"`)) {
				return errors.New("controller requires direct text authoring")
			}
		}
		if !found {
			return fmt.Errorf("controller tool not advertised: %s", operation)
		}
	}
	return nil
}

func waitExperimentHost(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// next consumes one actual result before issuing the next controller-owned operation.
func (c *experimentController) next(ctx context.Context, request *model.Request) (control.Operation, any, error) {
	if c.step >= 10 {
		return c.executionStep(ctx, request)
	}
	switch c.step {
	case 0:
		if err := checkControllerSchemas(request); err != nil {
			return "", nil, err
		}
		return control.DiscoverOperation, struct{}{}, nil
	case 1:
		discovered, err := decodeExperimentResult[control.Discovery](request)
		if err != nil {
			return "", nil, err
		}
		if discovered.Surface == nil || len(discovered.Tasks) != 2 {
			return "", nil, errors.New("controller needs discovered baseline and tasks")
		}
		c.baseline = discovered.Surface.BaselineDigest
		c.task = discovered.Tasks[0].ID
		c.cooperativeTask = discovered.Tasks[1].ID
		return control.CreateOperation, control.CreateRequest{Baseline: c.baseline}, nil
	case 2:
		draft, err := decodeExperimentResult[control.CandidateInfo](request)
		if err != nil {
			return "", nil, err
		}
		c.draft = draft
		return control.GetOperation, control.GetCandidateRequest{DraftID: c.draft.DraftID}, nil
	case 3:
		current, err := decodeExperimentResult[control.CandidateInfo](request)
		if err != nil {
			return "", nil, err
		}
		if current.Revision != c.draft.Revision {
			return "", nil, errors.New("unexpected draft revision")
		}
		return control.ReadOperation, control.ReadCandidateRequest{DraftID: current.DraftID, ExpectedRevision: current.Revision, Component: "helper.go"}, nil
	case 4:
		source, err := decodeExperimentResult[control.CandidateRead](request)
		if err != nil {
			return "", nil, err
		}
		if !strings.Contains(string(source.Data), "package main") {
			return "", nil, errors.New("baseline source was not read")
		}
		invalid := "package main\nfunc main("
		return control.EditOperation, c.helperEdit(invalid), nil
	case 5:
		result, err := latestExperimentResult(request)
		if err != nil {
			return "", nil, err
		}
		text, ok := result.Content.(string)
		if !result.IsError || !ok {
			return "", nil, errors.New("invalid source edit was not rejected")
		}
		var failure control.Error
		if err := json.Unmarshal([]byte(text), &failure); err != nil {
			return "", nil, err
		}
		if failure.Code != control.InvalidInput || failure.Recovery == "" {
			return "", nil, errors.New("rejected edit has no repair guidance")
		}
		c.invalidObserved = true
		return control.EditOperation, c.helperEdit(controllerHelperSource), nil
	case 6:
		current, err := decodeExperimentResult[control.CandidateInfo](request)
		if err != nil {
			return "", nil, err
		}
		if current.Revision != c.draft.Revision+1 {
			return "", nil, errors.New("failed edit changed revision")
		}
		c.draft = current
		skill := experimentSkill + "Return the helper's actual stdout as the deliverable.\n"
		return control.EditOperation, control.EditRequest{DraftID: current.DraftID, ExpectedRevision: current.Revision, Operation: candidate.Put, Name: "SKILL.md", Slot: "skills", MediaType: "text/markdown", Content: &skill, Dependencies: []string{"helper.go"}}, nil
	case 7:
		current, err := decodeExperimentResult[control.CandidateInfo](request)
		if err != nil {
			return "", nil, err
		}
		c.draft = current
		return control.ValidateOperation, control.RevisionRequest{DraftID: current.DraftID, ExpectedRevision: current.Revision}, nil
	case 8:
		checked, err := decodeExperimentResult[control.Validation](request)
		if err != nil {
			return "", nil, err
		}
		if !checked.Valid {
			return "", nil, errors.New("candidate validation failed")
		}
		return control.FreezeOperation, control.RevisionRequest{DraftID: c.draft.DraftID, ExpectedRevision: c.draft.Revision}, nil
	case 9:
		frozen, err := decodeExperimentResult[control.Frozen](request)
		if err != nil {
			return "", nil, err
		}
		c.candidate = frozen.Candidate
		return control.SubmitOperation, control.SubmitRequest{RequestKey: "controller-source", Candidate: c.candidate, TaskID: c.task}, nil
	default:
		return "", nil, errors.New("unexpected controller step")
	}
}

func (c *experimentController) executionStep(ctx context.Context, request *model.Request) (control.Operation, any, error) {
	switch c.step {
	case 10:
		job, err := decodeExperimentResult[control.Job](request)
		if err != nil {
			return "", nil, err
		}
		if job.State != control.Queued {
			return "", nil, errors.New("submission did not remain queued")
		}
		c.jobID = job.ID
		return control.StatusOperation, control.JobRequest{JobID: c.jobID}, nil
	case 11:
		job, err := decodeExperimentResult[control.Job](request)
		if err != nil {
			return "", nil, err
		}
		if job.State != control.Queued {
			return "", nil, errors.New("poll did not observe queued job")
		}
		c.firstJob <- job.ID
		if err := waitExperimentHost(ctx, c.fixture.firstDone); err != nil {
			return "", nil, err
		}
		return control.StatusOperation, control.JobRequest{JobID: c.jobID}, nil
	case 12:
		job, err := decodeExperimentResult[control.Job](request)
		if err != nil {
			return "", nil, err
		}
		if job.State != control.Succeeded || !job.EvidenceAvailable {
			return "", nil, errors.New("finished job lacks committed evidence")
		}
		return control.ListEvidenceOperation, control.EvidenceListRequest{JobID: c.jobID, Limit: 100}, nil
	case 13:
		page, err := decodeExperimentResult[inspect.Page](request)
		if err != nil {
			return "", nil, err
		}
		for _, entry := range page.Entries {
			if entry.Kind == inspect.Deliverable && entry.State == inspect.Available {
				return control.ReadEvidenceOperation, control.EvidenceReadRequest{JobID: c.jobID, Locator: entry.Locator}, nil
			}
		}
		return "", nil, errors.New("retained deliverable not listed")
	case 14:
		output, err := decodeExperimentResult[inspect.ReadResult](request)
		if err != nil {
			return "", nil, err
		}
		if output.Entry.State != inspect.Available || output.Truncated {
			return "", nil, errors.New("deliverable read is incomplete")
		}
		c.inspected = bytes.Clone(output.Data)
		return control.SubmitOperation, control.SubmitRequest{RequestKey: "controller-cancel", Candidate: c.candidate, TaskID: c.cooperativeTask}, nil
	default:
		return c.cancellationStep(ctx, request)
	}
}

func (c *experimentController) cancellationStep(ctx context.Context, request *model.Request) (control.Operation, any, error) {
	if c.step == 21 {
		compared, err := decodeExperimentResult[control.Comparison](request)
		if err != nil {
			return "", nil, err
		}
		if compared.Status != "not_comparable" || compared.Baseline.Quality != nil || compared.Candidate.Quality != nil {
			return "", nil, errors.New("probe comparison invented quality")
		}
		c.comparison = compared
		return "", nil, nil
	}
	job, err := decodeExperimentResult[control.Job](request)
	if err != nil {
		return "", nil, err
	}
	switch c.step {
	case 15:
		if job.State != control.Queued {
			return "", nil, errors.New("cooperative submission did not queue")
		}
		c.cancelID = job.ID
	case 16:
		if job.State != control.Queued {
			return "", nil, errors.New("cooperative job ran without host orchestration")
		}
		c.secondJob <- job.ID
		if err := waitExperimentHost(ctx, c.fixture.secondStarted); err != nil {
			return "", nil, err
		}
	case 17:
		if job.State != control.Running {
			return "", nil, errors.New("cooperative job was not running")
		}
		return control.CancelOperation, control.JobRequest{JobID: c.cancelID}, nil
	case 18:
		if job.State != control.CancelRequested {
			return "", nil, errors.New("cancel response falsely claimed stoppedness")
		}
	case 19:
		if job.State != control.CancelRequested {
			return "", nil, errors.New("cancel poll falsely claimed stoppedness")
		}
		close(c.cancelPolled)
		if err := waitExperimentHost(ctx, c.fixture.secondDone); err != nil {
			return "", nil, err
		}
	case 20:
		if job.State != control.Canceled {
			return "", nil, errors.New("cooperative finalization did not confirm canceled")
		}
		return control.CompareOperation, control.CompareRequest{BaselineJobID: c.jobID, CandidateJobID: c.cancelID}, nil
	default:
		return "", nil, errors.New("unexpected cancellation step")
	}
	return control.StatusOperation, control.JobRequest{JobID: c.cancelID}, nil
}

func (c *experimentController) helperEdit(source string) control.EditRequest {
	return control.EditRequest{DraftID: c.draft.DraftID, ExpectedRevision: c.draft.Revision, Operation: candidate.Put, Name: "helper.go", Slot: "helpers", MediaType: "text/x-go", Content: &source}
}

func (p experimentControllerPlanner) complete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("controller")
	if !ok {
		return nil, errors.New("controller model missing")
	}
	definitions := make([]*model.ToolDefinition, 0, len(p.specs))
	for _, spec := range p.specs {
		var schema any
		if err := json.Unmarshal(spec.Payload.Schema, &schema); err != nil {
			return nil, err
		}
		definitions = append(definitions, &model.ToolDefinition{Name: spec.Name.String(), Description: spec.Description, InputSchema: schema})
	}
	response, err := client.Complete(ctx, &model.Request{Model: "deterministic-controller", Messages: messages, Tools: definitions})
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
