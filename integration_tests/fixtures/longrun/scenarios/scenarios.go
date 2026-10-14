package scenarios

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"time"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evidence"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// New creates one scenario from an immutable pool.
func New(id string, items eval.Dataset) eval.Scenario {
	return eval.Scenario{ID: id, Items: items, Timeout: 5 * time.Minute, Drive: drive, Assert: assertAttempt}
}
func drive(ctx context.Context, h *agenttest.Harness, item eval.Item) (*agenttest.SessionResult, error) {
	m, err := env.Materialize(item)
	if err != nil {
		return nil, err
	}
	stubTools(h, m)
	s, err := h.OpenSession(ctx)
	if err != nil {
		return nil, err
	}
	id := "longrun." + m.Parameters.Template
	switch m.Parameters.Scenario {
	case "no_summariser":
		id += "_nosum"
	case "summariser_rejected":
		id += "_reject"
	}
	settings := ContextFor(m.Parameters.Scenario)
	var history agenttest.Input
	for _, input := range m.Inputs {
		history = append(history, input...)
		result, err := s.Run(ctx, agent.Ident(id), slices.Clone(history), settings.Apply())
		if err != nil {
			return s.Result(), err
		}
		// An agent error is an outcome; only harness errors abort Drive.
		completed := result.RunErr == nil
		if !completed {
			break
		}
		if result.Output != nil && result.Output.Final != nil {
			history = append(history, result.Output.Final)
		}
	}
	return s.Result(), nil
}
func assertAttempt(_ context.Context, a *eval.Attempt) (eval.Assertions, error) {
	m, err := env.Materialize(a.Item)
	if err != nil {
		return eval.Assertions{}, err
	}
	records := make([]*runrecord.RunRecord, 0, len(a.Session.Runs))
	succeeded := true
	for _, r := range a.Session.Runs {
		records = append(records, r.Record)
		succeeded = succeeded && r.RunErr == nil
	}
	return Measures(m, records, succeeded)
}

// ContextFor fixes the drive overrides bound into each release freeze.
func ContextFor(id string) ScenarioContext {
	settings := ScenarioContext{PretendWindow: 24000}
	if id == "model_change" {
		settings.PretendWindow = 8000
	}
	if id == "recall_pressure" {
		settings = ScenarioContext{PretendWindow: 8000, RecallDiscoveryCalls: 3, RecallReadCalls: 3}
	}
	return settings
}

// Measures recomputes every reserved measure from materialised truth and exports.
func Measures(m *env.Materialized, records []*runrecord.RunRecord, succeeded bool) (eval.Assertions, error) {
	threshold := false
	for _, rec := range records {
		for _, r := range rec.Runs {
			if r.ContextSettings != nil && r.ContextSettings.Threshold {
				threshold = true
			}
		}
	}
	classified, err := evidence.Classify(records, m.Spec, threshold)
	if err != nil {
		return eval.Assertions{}, err
	}
	ok, err := actionOK(m, records)
	if err != nil {
		return eval.Assertions{}, err
	}
	checks := classified.Checks(succeeded, ok)
	var out eval.Assertions
	for _, name := range []string{"task_success", "no_unsupported_claim"} {
		c := expect.Check{Name: name, Passed: checks[name]}
		if !c.Passed {
			c.Diagnostic = fmt.Sprintf("%s: %s", m.Parameters.Scenario, name)
		}
		out.Checks = append(out.Checks, c)
	}
	scores := classified.Scores()
	for _, name := range []string{"evidence_used", "evidence_absent", "need_found", "recall_qualifying", "recall_success", "recall_exhausted", "compactions", "summary_rejections", "overflow_recoveries", "recall_calls"} {
		out.Scores = append(out.Scores, expect.Score{Name: name, Value: scores[name]})
	}
	for _, source := range classified.Sources {
		out.Scores = append(out.Scores, expect.Score{Name: "source_" + string(source), Value: 1})
	}
	return out, nil
}
func actionOK(m *env.Materialized, records []*runrecord.RunRecord) (bool, error) {
	found := false
	for _, rec := range records {
		for _, r := range rec.Runs {
			for _, msg := range r.Transcript {
				for _, part := range msg.Parts {
					use, ok := part.(model.ToolUsePart)
					if !ok || use.Name != m.Action {
						continue
					}
					data, err := json.Marshal(use.Input)
					if err != nil {
						return false, err
					}
					var args struct {
						Step   string `json:"step"`
						Target string `json:"target"`
					}
					if err := json.Unmarshal(data, &args); err != nil {
						return false, err
					}
					for _, decoy := range m.Spec.Decoys {
						if strings.Contains(args.Target, decoy) {
							return false, nil
						}
					}
					if args.Step == "final" {
						found = true
						for _, token := range m.Spec.Answer {
							if !strings.Contains(args.Target, token) {
								return false, nil
							}
						}
					}
				}
			}
		}
	}
	return found, nil
}
