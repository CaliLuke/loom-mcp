package env

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/evidence"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Directive is the model-readable next action, never an answer oracle.
type Directive struct {
	Tool    string   `json:"tool"`
	Step    string   `json:"step"`
	Keys    []string `json:"keys,omitempty"`
	Then    string   `json:"then,omitempty"`
	Exhaust bool     `json:"exhaust,omitempty"`
}

// Materialized owns all caller inputs, tool results and scoring truth for an item.
type Materialized struct {
	Parameters      Parameters
	Inputs          []agenttest.Input
	Results         map[string]string
	Spec            evidence.Spec
	Action          string
	TranscriptBytes int
}

// TokenGrammar identifies planted and fabricated answer claims.
var TokenGrammar = regexp.MustCompile(`[A-Z]{3}-[0-9A-Z]{5}`)

// Materialize builds the complete environment solely from versioned item data.
func Materialize(item eval.Item) (*Materialized, error) {
	var p Parameters
	if err := json.Unmarshal(item.Data, &p); err != nil {
		return nil, err
	}
	var domain *Domain
	for _, d := range Domains() {
		if d.Name == p.Template {
			domain = &d
			break
		}
	}
	if domain == nil || p.Wording < 0 || p.Wording > 2 || p.Steps < 12 || p.ResultBytes < 1000 {
		return nil, fmt.Errorf("invalid materializer parameters for %s", item.ID)
	}
	// V4 expands the historical base workload for repeated compaction coverage
	// under shipped defaults. Raw pools and their historical interpretation stay
	// unchanged; materialized parameters expose the actual executed length.
	if p.Scenario == "model_change" {
		p.Steps *= 4
	} else {
		p.Steps *= 2
	}
	m := &Materialized{Parameters: p, Results: map[string]string{}, Action: toolName(p.Template, domain.Action), Spec: evidence.Spec{Evidence: map[evidence.EvidenceID]evidence.Evidence{}, Grammar: TokenGrammar}}
	token := func(key string) string {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%s/%s/%s", p.Seed, p.Scenario, p.Template, key)))
		return fmt.Sprintf("%s-%X", strings.ToUpper(key[:3]), sum[:3])[:9]
	}
	keys := []string{"target"}
	if strings.HasPrefix(p.Scenario, "obsolete_") {
		keys = []string{"staging"}
	}
	if strings.HasPrefix(p.Scenario, "multimodal_") {
		keys = []string{"image", "pdf"}
	}
	if p.Scenario == "decisions_dependencies" {
		keys = []string{"decision", "invoice_a", "invoice_b", "invoice_c"}
	}
	if p.Scenario == "recall_pressure" {
		keys = []string{"fact_a", "fact_b", "fact_c", "fact_d", "fact_e", "fact_f"}
	}
	for _, key := range keys {
		value := token(key)
		m.Spec.Evidence[evidence.EvidenceID(key)] = evidence.Evidence{Value: value}
		m.Spec.Answer = append(m.Spec.Answer, value)
	}
	runs := 1
	if p.Scenario == "obsolete_cross_run" || p.Scenario == "decisions_dependencies" {
		runs = 2
	}
	if p.Scenario == "session_three_runs" {
		runs = 3
	}
	for run := range runs {
		if err := m.buildRun(*domain, run, runs, keys); err != nil {
			return nil, err
		}
	}
	for _, in := range m.Inputs {
		for _, msg := range in {
			for _, part := range msg.Parts {
				if text, ok := part.(model.TextPart); ok {
					m.TranscriptBytes += len(text.Text)
				}
			}
		}
	}
	for _, result := range m.Results {
		m.TranscriptBytes += len(result)
	}
	return m, nil
}

func (m *Materialized) buildRun(d Domain, run, runs int, keys []string) error {
	p := m.Parameters
	prefix := fmt.Sprintf("r%d", run+1)
	input := agenttest.User(d.Wordings[p.Wording] + " Follow NEXT instructions. Resolve NEED keys from received evidence; recall if necessary. Never guess tokens.")
	if p.Scenario == "summariser_rejected" || (p.Scenario == "decisions_dependencies" && run == 1) {
		for i := range 30 {
			input = append(input, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "The audit continues."}}}, agenttest.User(fmt.Sprintf("Audit context %d. %s", i, strings.Repeat("review history ", 120)))[0])
		}
	}
	obsolete := strings.HasPrefix(p.Scenario, "obsolete_")
	if obsolete {
		decoy := "OLD-00000"
		m.Spec.Decoys = []string{decoy}
		input[0].Parts = append(input[0].Parts, model.TextPart{Text: "Production target = " + decoy})
		for i := 2; i <= 60; i++ {
			if i%2 == 0 {
				input = append(input, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "Acknowledged the audit note."}}})
				continue
			}
			line := fmt.Sprintf("Audit message %d. %s", i, strings.Repeat("background note ", 120))
			if run == 0 && (i == 1 || (p.Scenario == "obsolete_cross_run" && i == 57)) {
				line = "Production target = " + decoy
			}
			correction := (p.Scenario != "obsolete_cross_run" && p.Scenario != "obsolete_index_line" && i == 5) || (p.Scenario == "obsolete_cross_run" && run == 1 && i == 5)
			if correction {
				line = "Correction: use staging only. staging = " + m.Spec.Answer[0]
				if p.Scenario == "obsolete_buried" {
					line = strings.Repeat("long context ", 30) + line
				}
			}
			if p.Scenario == "obsolete_index_line" && i == 57 {
				line = "Correction: use staging only. staging = " + m.Spec.Answer[0] + " " + strings.Repeat("approval context ", 120)
			}
			input = append(input, agenttest.User(line)...)
		}
	}
	if p.Scenario == "session_three_runs" && run == 1 {
		input[0].Parts = append(input[0].Parts, model.TextPart{Text: "Continue the prior decision without changing it."})
	}
	start, err := directive("NEXT:", Directive{Tool: toolName(d.Name, d.List), Step: prefix + ".0"})
	if err != nil {
		return err
	}
	if strings.HasPrefix(p.Scenario, "multimodal_") {
		for i, key := range keys {
			format := "png"
			if key == "pdf" {
				format = "pdf"
			}
			value := m.Spec.Evidence[evidence.EvidenceID(key)].Value
			data, err := RenderAsset(format, value)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			m.Spec.Evidence[evidence.EvidenceID(key)] = evidence.Evidence{Asset: fmt.Sprintf("%x", sum), Token: value}
			var part model.Part = model.ImagePart{Format: "png", Bytes: data}
			if format == "pdf" {
				part = model.DocumentPart{Format: "pdf", Name: "manifest", Bytes: data}
			}
			if i == 0 {
				input[0].Parts = append(input[0].Parts, model.TextPart{Text: "image = sha256:" + fmt.Sprintf("%x", sum)}, part)
			} else {
				input = append(input, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "The image is received."}}}, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "pdf = sha256:" + fmt.Sprintf("%x", sum)}, part}})
			}
		}
	}
	if strings.HasPrefix(p.Scenario, "multimodal_") {
		for i := 0; i < 40; i++ {
			input = append(input, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "Continue reviewing the attachments later."}}}, agenttest.User(fmt.Sprintf("Attachment audit note %d. %s", i, strings.Repeat("supporting context ", 100)))[0])
		}
	}

	if input[len(input)-1].Role == model.ConversationRoleUser {
		input[len(input)-1].Parts = append(input[len(input)-1].Parts, model.TextPart{Text: start})
	} else {
		input = append(input, agenttest.User(start)...)
	}
	m.Inputs = append(m.Inputs, input)
	for step := 0; step < p.Steps; step++ {
		key := fmt.Sprintf("%s.%d", prefix, step)
		body := strings.Repeat("ordinary work log. ", (p.ResultBytes/19)+1)
		if step < len(keys) && !strings.HasPrefix(p.Scenario, "multimodal_") && !obsolete && !(p.Scenario == "session_three_runs" && run != 0) {
			fact := keys[step] + " = " + m.Spec.Evidence[evidence.EvidenceID(keys[step])].Value + "\n"
			if p.Scenario == "recall_pressure" {
				// Keep the facts outside the 8k window's summary prefix so a
				// later summary cannot replace an outstanding evidence read.
				body += "\n" + fact
			} else if p.Scenario != "decisions_dependencies" || (run == 1 && step > 0) {
				body = fact + body
			}
			if p.Scenario == "large_result_middle" {
				// Leave room below 60 KB for the directive and JSON framing.
				// Equal padding keeps the fact in the middle of the result.
				padding := strings.Repeat("large record ", 2250)
				body = padding + fact + padding
			}
		}
		if p.Scenario == "session_three_runs" && run == 0 && step == p.Steps-1 {
			body = "Decision: target = " + m.Spec.Answer[0] + "\n"
		}
		if p.Scenario == "decisions_dependencies" && run == 0 && step == p.Steps-1 {
			body = "Proposed option: choice = " + m.Spec.Answer[0] + "\n"
		}
		if p.Scenario == "model_change" && step >= p.Steps/2 {
			body += "\n[[switch-model]]"
		}
		next := Directive{Tool: toolName(d.Name, d.Read), Step: fmt.Sprintf("%s.%d", prefix, step+1)}
		marker := "NEXT:"
		needPoint := prefix
		if p.Scenario == "recall_pressure" {
			needPoint += ".final"
		}
		if step == p.Steps-1 {
			if run == runs-1 {
				marker = "NEED:"
				next = Directive{Tool: m.Action, Step: "final", Keys: keys}
				m.addNeed(needPoint, keys)
			} else {
				next = Directive{Step: "done"}
				if p.Scenario == "decisions_dependencies" && run == 0 {
					next.Keys = []string{"choice"}
				}
				if p.Scenario == "session_three_runs" && run == 0 {
					next.Keys = []string{"target"}
				}
			}
		}
		if p.Scenario == "recall_pressure" && step == p.Steps/2 {
			marker = "NEED:"
			next.Keys = keys[:3]
			next.Exhaust = true
			needPoint = prefix + ".middle"
			m.addNeed(needPoint, keys[:3])
		}
		text, err := directive(marker, next)
		if err != nil {
			return err
		}
		trigger := ""
		if marker == "NEED:" {
			trigger = "NEED_POINT:" + needPoint
		}
		m.Results[key] = trigger + "\n" + text + "\n" + body
	}
	return nil
}
func (m *Materialized) addNeed(trigger string, keys []string) {
	need := evidence.Need{Trigger: "NEED_POINT:" + trigger}
	for _, key := range keys {
		need.Evidence = append(need.Evidence, evidence.EvidenceID(key))
	}
	m.Spec.Needs = append(m.Spec.Needs, need)
}
func toolName(domain, name string) string {
	return domain + "." + name
}
func directive(prefix string, d Directive) (string, error) {
	data, err := json.Marshal(d)
	return prefix + string(data), err
}
