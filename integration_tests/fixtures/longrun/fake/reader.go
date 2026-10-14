// Package fake implements request-reading offline models, without scoring access.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"strings"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

// Faults are deliberately broken model behaviours for acceptance tests.
type Faults struct{ NoRecall, Fabricate, DropFacts, PreferDecoy bool }

// Reader follows NEXT and NEED directives using only the current model request.
type Reader struct {
	Faults  Faults
	Profile *ModelProfile
}

// Complete executes one deterministic model turn.
func (r *Reader) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text, err := RequestText(req)
	if err != nil {
		return nil, err
	}
	directive, need, err := latestDirective(req)
	if err != nil {
		return nil, err
	}
	if directive.Step == "done" {
		if len(directive.Keys) > 0 {
			value := fact(text, directive.Keys[0], false)
			key := directive.Keys[0]
			if key == "choice" {
				return response(req, "I reviewed the proposed option.\nDecision: decision = "+value, nil)
			}
			return response(req, "Decision: "+key+" = "+value, nil)
		}
		return response(req, "Complete.", nil)
	}
	if directive.Step == "answer" {
		return response(req, directive.Then, nil)
	}
	if !need {
		return action(req, directive, "")
	}
	if !r.Faults.NoRecall {
		if next, ok, err := nextRecallRead(req, directive.Keys, text); err != nil {
			return nil, err
		} else if ok {
			return call(req, tools.RecallContext, next)
		}
	}
	values := make([]string, 0, len(directive.Keys))
	for _, key := range directive.Keys {
		value := fact(text, key, r.Faults.PreferDecoy)
		if r.Faults.Fabricate {
			value = "BAD-99999"
		}
		if value == "" && !r.Faults.NoRecall && offered(req, tools.RecallContext) && !recallStopped(req) {
			return call(req, tools.RecallContext, recall.Input{Find: &recall.FindInput{Text: key + " = "}})
		}
		if value == "" {
			value = "unknown"
		}
		values = append(values, value)
	}
	if directive.Exhaust && !r.Faults.NoRecall && offered(req, tools.RecallContext) {
		_, last, found, err := lastRecall(req)
		if err != nil {
			return nil, err
		}
		if !found || last.Code != recall.CodeBudgetExhausted {
			return call(req, tools.RecallContext, recall.Input{Find: &recall.FindInput{Text: "pressure probe: no evidence expected"}})
		}
	}
	return action(req, directive, strings.Join(values, " "))
}

// Stream is intentionally unsupported; the fixture planner uses Complete.
func (r *Reader) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}

func latestDirective(req *model.Request) (env.Directive, bool, error) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m == nil {
			continue
		}
		for _, part := range m.Parts {
			if result, ok := part.(model.ToolResultPart); ok {
				if omitted, ok := result.Content.(map[string]any); ok && omitted["omitted"] == true {
					return env.Directive{}, false, fmt.Errorf("Reader: size-limit omission removed the next work step")
				}
			}
		}
		text, err := RequestText(&model.Request{Messages: []*model.Message{m}})
		if err != nil {
			return env.Directive{}, false, err
		}
		lines := strings.Split(text, "\n")
		for j := len(lines) - 1; j >= 0; j-- {
			line := lines[j]
			need := strings.HasPrefix(line, "NEED:")
			if !need && !strings.HasPrefix(line, "NEXT:") {
				continue
			}
			var d env.Directive
			if err := json.Unmarshal([]byte(line[5:]), &d); err != nil {
				return d, false, err
			}
			return d, need, nil
		}
	}
	return env.Directive{}, false, fmt.Errorf("Reader: no directive in request")
}
func fact(text, key string, preferDecoy bool) string {
	re := regexp.MustCompile(regexp.QuoteMeta(key) + ` = ([A-Z]{3}-[0-9A-Z]{5})`)
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return ""
	}
	if preferDecoy {
		return matches[0][1]
	}
	return matches[len(matches)-1][1]
}
func offered(req *model.Request, name tools.Ident) bool {
	for _, d := range req.Tools {
		if d.Name == string(name) {
			return true
		}
	}
	return false
}
func action(req *model.Request, d env.Directive, target string) (*model.Response, error) {
	return call(req, tools.Ident(d.Tool), struct {
		Step   string `json:"step"`
		Target string `json:"target"`
	}{d.Step, target})
}
func call(req *model.Request, name tools.Ident, payload any) (*model.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := json.Marshal(req.Messages, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(append(request, data...))
	text := ""
	if name == tools.RecallContext {
		d, need, err := latestDirective(req)
		if err != nil {
			return nil, err
		}
		if need {
			b, err := json.Marshal(d)
			if err != nil {
				return nil, err
			}
			text = "NEED:" + string(b)
			received, err := RequestText(req)
			if err != nil {
				return nil, err
			}
			for _, key := range d.Keys {
				if value := fact(received, key, false); value != "" {
					text += "\nWorking note: " + key + " = " + value
				}
			}
		}
	}
	return response(req, text, []model.ToolCall{{Name: name, Payload: data, ID: fmt.Sprintf("call-%x", digest[:8])}})
}
func response(req *model.Request, text string, calls []model.ToolCall) (*model.Response, error) {
	b, err := json.Marshal(req.Messages)
	if err != nil {
		return nil, err
	}
	r := &model.Response{ToolCalls: calls, StopReason: "end_turn", Usage: model.TokenUsage{InputTokens: (len(b) + 3) / 4, OutputTokens: 10}}
	r.Usage.TotalTokens = r.Usage.InputTokens + r.Usage.OutputTokens
	if len(calls) > 0 {
		r.StopReason = "tool_use"
	}
	if text != "" || len(calls) == 0 {
		r.Content = []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: text}}}}
	}
	return r, nil
}
