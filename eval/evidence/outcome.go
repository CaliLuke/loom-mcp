package evidence

import (
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// Checks returns reserved deterministic check values. The caller supplies agent
// completion and its scenario-specific final action check; neither is judged.
func (r Result) Checks(runsSucceeded, actionOK bool) map[string]bool {
	return map[string]bool{"task_success": runsSucceeded && actionOK && r.Used, "no_unsupported_claim": !r.UnsupportedClaim}
}

// Scores returns the reserved numeric evidence and context-operation measures.
func (r Result) Scores() map[string]float64 {
	return map[string]float64{"evidence_used": flag(r.Used), "evidence_absent": flag(r.AbsentWhenNeeded), "need_found": flag(r.NeedFound), "recall_qualifying": flag(r.RecallQualifying), "recall_success": flag(r.RecallSuccess), "recall_exhausted": flag(r.Exhausted), "compactions": float64(r.Compactions), "summary_rejections": float64(r.SummaryRejections), "overflow_recoveries": float64(r.OverflowRecoveries), "recall_calls": float64(r.RecallCalls)}
}

func finalText(records []*runrecord.RunRecord) (string, error) {
	runs, err := sessionRoots(records)
	if err != nil {
		return "", err
	}
	var answer, action string
	for _, run := range runs {
		for _, m := range run.Transcript {
			if m == nil || m.Role != model.ConversationRoleAssistant {
				continue
			}
			var text strings.Builder
			for _, p := range m.Parts {
				switch v := p.(type) {
				case model.TextPart:
					text.WriteString(v.Text)
				case model.CitationsPart:
					text.WriteString(v.Text)
				case model.ToolUsePart:
					if !recallName(v.Name) {
						value, err := jsonText(v.Input)
						if err != nil {
							return "", err
						}
						action = value
					}
				}
			}
			if text.Len() > 0 {
				answer = text.String()
			}
		}
	}
	return answer + "\n" + action, nil
}

// unsupported checks claims against received input of adopted planner calls.
// Stored output bytes and earlier assistant claims cannot support themselves.
func unsupported(answer string, s Spec, calls []observedCall) bool {
	for _, decoy := range s.Decoys {
		if decoy != "" && strings.Contains(answer, decoy) {
			return true
		}
	}
	if s.Grammar == nil {
		return false
	}
	for _, token := range s.Grammar.FindAllString(answer, -1) {
		found := false
		for _, call := range calls {
			for _, input := range call.fragments {
				if input.received && supportsToken(input, token, s.Evidence) {
					found = true
					break
				}
			}
		}
		if !found {
			return true
		}
	}
	return false
}
func countEvents(out *Result, runs []runrecord.Run) error {
	for _, r := range runs {
		out.SummaryRejections += r.Summary.Rejections
		for _, e := range r.Events {
			if e == nil {
				continue
			}
			switch {
			case e.Type == hooks.ContextCompacted:
				out.Compactions++
			case e.Type == hooks.ContextOverflow:
				out.OverflowRecoveries++
			case e.Type == hooks.ContextRecalled:
				out.RecallCalls++
				var payload struct {
					Outcome string `json:"outcome"`
				}
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					return err
				}
				out.Exhausted = out.Exhausted || payload.Outcome == "exhausted"
			default:
			}
		}
	}
	return nil
}

func flag(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func sessionRoots(records []*runrecord.RunRecord) ([]runrecord.Run, error) {
	roots := make([]runrecord.Run, 0, len(records))
	for _, record := range records {
		found := false
		for _, run := range record.Runs {
			if run.RunID == record.RootRunID {
				roots = append(roots, run)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("evidence: record missing root run %q", record.RootRunID)
		}
	}
	return roots, nil
}
