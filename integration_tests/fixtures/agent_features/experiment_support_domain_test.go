package agentfeatures_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type supportDomain struct {
	dir      string
	facts    supportFacts
	proposed string
}

type supportOutcome struct {
	Analysis supportAnalysis `json:"analysis"`
	Review   string          `json:"review"`
	Body     string          `json:"body"`
	Version  int             `json:"version"`
}

func supportBody(analysis supportAnalysis, review string) string {
	facts := analysis.Facts
	if facts.Kind == "document" {
		return fmt.Sprintf("# %s: %s\nFor %s\n%s\n%s\n%s\n", facts.ID, facts.Label, facts.Audience, analysis.Summary, review, facts.Protected)
	}
	return fmt.Sprintf("### What's included\n- %s for %s (%s, draft=%t)\n- %s\n- %s\n", facts.Label, facts.Audience, facts.ID, facts.Draft, analysis.Summary, review)
}

// registration exposes task-bound operations; candidate instructions cannot grant authority.
func (d *supportDomain) registration(guidance string) runtime.ToolsetRegistration {
	names := []string{"documents.read_document", "documents.propose_edit", "documents.accept_edit"}
	toolset := "documents"
	if d.facts.Kind == "graph" {
		names = []string{"graph.search_tools", "graph.call_tool", "graph.record_analysis"}
		toolset = "graph"
	}
	specs := make([]tools.ToolSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, tools.ToolSpec{Name: tools.Ident(name), Toolset: toolset, Description: guidance, Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec, Schema: []byte(`{"type":"object"}`)}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}})
	}
	return runtime.ToolsetRegistration{Name: toolset, Specs: specs, Execute: d.execute}
}

func (d *supportDomain) execute(ctx context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var payload struct {
		ID       string          `json:"id"`
		Tool     string          `json:"tool"`
		Analysis supportAnalysis `json:"analysis"`
		Review   string          `json:"review"`
		Body     string          `json:"body"`
		Proposal string          `json:"proposal"`
	}
	if err := json.Unmarshal(call.Payload, &payload); err != nil {
		return nil, err
	}
	var output any
	switch call.Name.String() {
	case "graph.search_tools":
		output = map[string]string{"tool": "get_release"}
	case "documents.read_document", "graph.call_tool":
		if payload.ID != d.facts.ID || (call.Name == "graph.call_tool" && payload.Tool != "get_release") {
			return nil, errors.New("source not authorized")
		}
		source, err := os.ReadFile(filepath.Join(d.dir, "source.json"))
		if err != nil {
			return nil, err
		}
		var facts supportFacts
		if err := json.Unmarshal(source, &facts); err != nil {
			return nil, err
		}
		output = facts
	case "documents.propose_edit", "graph.record_analysis":
		if !reflect.DeepEqual(payload.Analysis.Facts, d.facts) || payload.Analysis.SourceDigest != supportDigest(supportJSON(d.facts)) || !strings.Contains(payload.Review, payload.Analysis.Summary) || !strings.Contains(payload.Review, d.facts.ID) {
			return nil, errors.New("analysis does not reflect current source and child review")
		}
		body := supportBody(payload.Analysis, payload.Review)
		if call.Name == "documents.propose_edit" {
			if payload.Body != body || !strings.Contains(body, d.facts.Protected) {
				return nil, errors.New("edit does not preserve protected current document")
			}
			d.proposed = body
			output = map[string]string{"proposal": "current-edit", "body": body}
		} else {
			outcome := supportOutcome{Analysis: payload.Analysis, Review: payload.Review, Body: body, Version: 1}
			if err := d.retain(outcome, "release.md"); err != nil {
				return nil, err
			}
			output = outcome
		}
	case "documents.accept_edit":
		if payload.Proposal != "current-edit" || d.proposed == "" {
			return nil, errors.New("no current proposal")
		}
		if payload.Body != d.proposed {
			return nil, errors.New("accepted bytes differ from proposal")
		}
		outcome := supportOutcome{Analysis: payload.Analysis, Review: payload.Review, Body: d.proposed, Version: 2}
		if err := d.retain(outcome, "document.md"); err != nil {
			return nil, err
		}
		output = outcome
	default:
		return nil, errors.New("unadvertised domain operation")
	}
	return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: output}), nil
}

func (d *supportDomain) retain(outcome supportOutcome, name string) error {
	if err := os.WriteFile(filepath.Join(d.dir, name), []byte(outcome.Body), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.dir, "receipt.json"), supportJSON(outcome), 0o600)
}
