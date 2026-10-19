package agentfeatures_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	delegated "example.com/agentfeatures/gen/features/agents/specialist/exports/delegated"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type supportSolver struct {
	kind     string
	id       string
	step     int
	facts    supportFacts
	analysis supportAnalysis
	review   string
	body     string
	calls    *atomic.Int64
}

type supportChildModel struct{}

type supportPlanner struct {
	name  string
	specs []tools.ToolSpec
	child bool
}

// Complete derives every later payload from actual preceding tool content.
func (s *supportSolver) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	s.calls.Add(1)
	if len(request.Tools) == 0 {
		return nil, fmt.Errorf("no advertised tools at solver step %d: %s", s.step, supportJSON(request.Messages[len(request.Messages)-1]))
	}
	var name string
	var payload any = map[string]any{}
	result, resultErr := latestExperimentResult(request)
	if s.step > 0 && resultErr != nil {
		return nil, resultErr
	}
	if s.step > 0 && result.IsError {
		return nil, fmt.Errorf("unexpected solver tool failure: %v", result.Content)
	}
	previous := ""
	if resultErr == nil {
		for _, message := range request.Messages {
			for _, part := range message.Parts {
				if use, ok := part.(model.ToolUsePart); ok && use.ID == result.ToolUseID {
					previous = use.Name
				}
			}
		}
	}
	switch previous {
	case "":
		if s.kind == "document" {
			name = "documents.read_document"
			payload = map[string]string{"id": s.id}
		} else {
			name = "graph.search_tools"
		}
	case "graph.search_tools":
		var found struct {
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal(supportJSON(result.Content), &found); err != nil {
			return nil, err
		}
		name = "graph.call_tool"
		payload = map[string]string{"tool": found.Tool, "id": s.id}
	case "documents.read_document", "graph.call_tool":
		if err := json.Unmarshal(supportJSON(result.Content), &s.facts); err != nil {
			return nil, err
		}
		name = "skills.list_skills"
	case "skills.list_skills":
		if !strings.Contains(string(supportJSON(result.Content)), "transform") {
			return nil, errors.New("skill absent")
		}
		name = "skills.load_skill"
		payload = map[string]string{"skill": "transform"}
	case "skills.load_skill":
		if !strings.Contains(string(supportJSON(result.Content)), "components.run_helper") {
			return nil, errors.New("helper not activated")
		}
		name = "components.run_helper"
		payload = map[string]string{"input": string(supportJSON(s.facts))}
	case "components.run_helper":
		var output struct {
			Stdout string `json:"stdout"`
		}
		if err := json.Unmarshal(supportJSON(result.Content), &output); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(output.Stdout), &s.analysis); err != nil {
			return nil, err
		}
		name = "delegated.summarize"
		payload = &delegated.SummarizePayload{Topic: output.Stdout}
	case "delegated.summarize":
		var child delegated.SummarizeResult
		if err := json.Unmarshal(supportJSON(result.Content), &child); err != nil {
			return nil, err
		}
		if !child.OK || child.Message == "" {
			return nil, errors.New("child supplied no review")
		}
		s.review = child.Message
		s.body = supportBody(s.analysis, s.review)
		payload = map[string]any{"analysis": s.analysis, "review": s.review, "body": s.body}
		if s.kind == "document" {
			name = "documents.propose_edit"
		} else {
			name = "graph.record_analysis"
		}
	case "documents.propose_edit":
		var proposal struct {
			Proposal string `json:"proposal"`
			Body     string `json:"body"`
		}
		if err := json.Unmarshal(supportJSON(result.Content), &proposal); err != nil {
			return nil, err
		}
		if proposal.Proposal == "" {
			return nil, errors.New("actual proposal missing")
		}
		name = "documents.accept_edit"
		payload = map[string]any{"proposal": proposal.Proposal, "body": proposal.Body, "analysis": s.analysis, "review": s.review}
	case "documents.accept_edit", "graph.record_analysis":
		var outcome supportOutcome
		if err := json.Unmarshal(supportJSON(result.Content), &outcome); err != nil {
			return nil, err
		}
		return supportRespond(ctx, request, agenttest.Respond(agenttest.Text(outcome.Body)))
	default:
		return nil, fmt.Errorf("unexpected prior tool %s", previous)
	}

	s.step++
	return supportRespond(ctx, request, agenttest.Respond(agenttest.RawToolCall(tools.Ident(name), rawjson.Message(supportJSON(payload))), agenttest.CallID(fmt.Sprintf("support-%d", s.step))))
}

// Stream is unused by the deterministic fixture planner.
func (*supportSolver) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, errors.New("support uses complete requests")
}

// Complete interprets only the authored bounded grammar and actual delegated payload.
func (supportChildModel) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	var instruction, topic string
	for _, message := range request.Messages {
		if message.Role == model.ConversationRoleSystem {
			instruction = experimentMessageText(message)
		}
		if message.Role == model.ConversationRoleUser && experimentMessageText(message) != "" {
			var payload delegated.SummarizePayload
			if err := json.Unmarshal([]byte(experimentMessageText(message)), &payload); err != nil {
				return nil, err
			}
			topic = payload.Topic
		}
	}
	section, err := supportSection(instruction)
	if err != nil {
		return nil, err
	}
	var analysis supportAnalysis
	if err := json.Unmarshal([]byte(topic), &analysis); err != nil {
		return nil, err
	}
	result, err := latestExperimentResult(request)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, errors.New("child current-source read failed")
	}
	var current supportFacts
	if err := json.Unmarshal(supportJSON(result.Content), &current); err != nil {
		return nil, err
	}
	if string(supportJSON(current)) != string(supportJSON(analysis.Facts)) {
		return nil, errors.New("child analysis differs from current source")
	}
	return supportRespond(ctx, request, agenttest.Respond(agenttest.Text(section+": "+analysis.Summary+" ["+current.ID+"]")))
}

// Stream is unused by the generated child's fixture planner.
func (supportChildModel) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, errors.New("support child uses complete requests")
}

// PlanStart binds the child to a current-source read before its review model runs.
func (p supportPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if p.child {
		var payload delegated.SummarizePayload
		for _, message := range in.Messages {
			if message.Role == model.ConversationRoleUser {
				if err := json.Unmarshal([]byte(experimentMessageText(message)), &payload); err != nil {
					return nil, err
				}
			}
		}
		var analysis supportAnalysis
		if err := json.Unmarshal([]byte(payload.Topic), &analysis); err != nil {
			return nil, err
		}
		name := "documents.read_document"
		args := map[string]string{"id": analysis.Facts.ID}
		if analysis.Facts.Kind == "graph" {
			name = "graph.call_tool"
			args["tool"] = "get_release"
		}
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: tools.Ident(name), ToolCallID: "child-current-source", Payload: rawjson.Message(supportJSON(args))}}}, nil
	}
	return p.complete(ctx, in.Agent, in.Messages, in.RunContext.RunID)
}

// PlanResume forwards actual tool outputs to the same per-run model.
func (p supportPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.complete(ctx, in.Agent, in.Messages, in.RunContext.RunID)
}

func supportRespond(ctx context.Context, request *model.Request, turn agenttest.Turn) (*model.Response, error) {
	script, err := agenttest.NewScript(turn)
	if err != nil {
		return nil, err
	}
	return script.Client().Complete(ctx, request)
}

func (p supportPlanner) complete(ctx context.Context, pc planner.PlannerContext, messages []*model.Message, runID string) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient(p.name)
	if !ok {
		return nil, errors.New("support model unavailable")
	}
	var definitions []*model.ToolDefinition
	for _, spec := range p.specs {
		var schema any = map[string]any{"type": "object"}
		if len(spec.Payload.Schema) > 0 {
			if err := json.Unmarshal(spec.Payload.Schema, &schema); err != nil {
				return nil, err
			}
		}
		definitions = append(definitions, &model.ToolDefinition{Name: spec.Name.String(), Description: spec.Description, InputSchema: schema})
	}
	response, err := client.Complete(ctx, &model.Request{RunID: runID, Model: p.name, Messages: messages, Tools: definitions})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{}
	for _, call := range response.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: call.Name, ToolCallID: call.ID, Payload: call.Payload})
	}
	if len(result.ToolCalls) > 0 {
		return result, nil
	}
	if p.child {
		data, err := delegated.SummarizeResultCodec.ToJSON(&delegated.SummarizeResult{OK: true, Message: experimentMessageText(&response.Content[0])})
		if err != nil {
			return nil, err
		}
		result.FinalToolResult = &planner.FinalToolResult{Result: rawjson.Message(data)}
	} else {
		result.FinalResponse = &planner.FinalResponse{Message: &response.Content[0]}
	}
	return result, nil
}
