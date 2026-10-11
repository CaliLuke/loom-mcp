package trajectory

import (
	"fmt"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
)

// Format selects the human-readable rendering syntax.
type Format string

// Limits bounds individual rendered values, steps per run and child nesting.
// Each zero field disables that limit. The root run has depth zero.
type Limits struct {
	MaxValueBytes int
	MaxSteps      int
	MaxDepth      int
}

// RenderOptions configures human-readable rendering; the zero format is text.
type RenderOptions struct {
	Format
	Limits
}

type renderer struct {
	opts RenderOptions
	out  strings.Builder
}

const (
	// FormatText renders plain text with two-space child indentation.
	FormatText Format = "text"
	// FormatMarkdown renders nested lists, fenced JSON and safely fenced plain text.
	FormatMarkdown Format = "markdown"
)

// DefaultLimits returns a 4096-byte value limit, 200 steps per run and depth 4.
func DefaultLimits() Limits {
	return Limits{MaxValueBytes: 4096, MaxSteps: 200, MaxDepth: 4}
}

// Render returns the run's causal steps, linked children, final answer and totals.
// It intentionally fails fast by panicking on a nil trajectory, nil Children
// elements or unsupported final message Part types.
func (t *Trajectory) Render(opts RenderOptions) string {
	r := renderer{opts: opts}
	r.run(t, "", 0)
	return r.out.String()
}

// Render returns runs in the session's caller-supplied order.
// It intentionally fails fast by panicking on nil Session or Children elements
// or unsupported final message Part types.
func (s Session) Render(opts RenderOptions) string {
	r := renderer{opts: opts}
	for i, run := range s {
		if i > 0 {
			r.out.WriteByte('\n')
		}
		r.run(run, "", 0)
	}
	return r.out.String()
}

func (r *renderer) run(t *Trajectory, indent string, depth int) {
	r.line(indent, "Run: "+t.RunID)
	r.line(indent, "Session: "+t.SessionID)
	r.line(indent, "Agent: "+string(t.AgentID))
	if t.TraceID != "" {
		r.line(indent, "Trace: "+t.TraceID)
	}
	status := t.Status
	if !t.StatusVerified {
		status += " (unverified)"
	}
	r.line(indent, "Status: "+status)
	count := len(t.Steps)
	if r.opts.MaxSteps > 0 && count > r.opts.MaxSteps {
		count = r.opts.MaxSteps
	}
	for i, step := range t.Steps[:count] {
		if i == 0 || step.Step != t.Steps[i-1].Step {
			r.line(indent, fmt.Sprintf("Step %d", step.Step))
		}
		r.step(step, indent+"  ", depth)
	}
	if count < len(t.Steps) {
		r.line(indent, fmt.Sprintf("[%d more steps omitted]", len(t.Steps)-count))
	}
	if t.Final != nil {
		r.line(indent, "Final answer:")
		for _, part := range t.Final.Parts {
			r.text(indent+"  ", "", finalPart(part))
		}
	}
	r.totals(t.Totals, indent)
}

func (r *renderer) step(s Step, indent string, depth int) {
	mark := ""
	if !s.Verified {
		mark = " (observed, unverified)"
	}
	switch {
	case s.Model != nil:
		r.model(s.Model, indent, mark)
	case s.Tool != nil:
		r.tool(s.Tool, indent, depth, mark)
	case s.Answer != nil:
		r.line(indent, "Answer: "+string(s.Answer.Kind)+mark)
		r.line(indent+"  ", "Input ID: "+s.Answer.InputID)
		r.value(indent+"  ", "Content", s.Answer.Content)
	case s.Event != nil:
		r.event(s, indent, mark)
	}
}

func (r *renderer) model(m *ModelStep, indent, mark string) {
	r.line(indent, fmt.Sprintf("Model: %d/%d %s%s", m.Key.Seq, m.Key.Attempt, m.Class, mark))
	fields := indent + "  "
	r.line(fields, "Activity: "+string(m.Activity))
	r.line(fields, "Model: "+m.Model)
	r.line(fields, "Model ID: "+m.ModelID)
	r.encoded(fields, "Usage", m.Usage)
	r.line(fields, "Latency: "+m.Latency.String())
	if m.Error != nil {
		r.line(fields, "Error kind: "+m.Error.Kind)
		r.text(fields, "Error", m.Error.Message)
	}
	for _, call := range m.Chosen {
		r.line(fields, "Chosen call: "+string(call.Name)+" ID: "+call.ID)
		r.value(fields+"  ", "Arguments", call.Payload)
	}
	r.line(fields, "System hash: "+shortHash(string(m.SystemHash)))
	r.line(fields, "Tools hash: "+shortHash(string(m.ToolsHash)))
	r.line(fields, "Request digest: "+shortHash(string(m.RequestDigest)))
}

func (r *renderer) tool(t *ToolStep, indent string, depth int, mark string) {
	r.line(indent, "Tool: "+string(t.Name)+mark)
	fields := indent + "  "
	r.line(fields, "Call ID: "+t.ToolCallID)
	if t.ParentToolCallID != "" {
		r.line(fields, "Parent call ID: "+t.ParentToolCallID)
	}
	r.value(fields, "Arguments", t.Arguments)
	r.value(fields, "Result", t.Result)
	if t.Bounds != nil {
		r.encoded(fields, "Bounds", t.Bounds)
	}
	if t.Error != nil {
		r.encoded(fields, "Error", t.Error)
	}
	r.line(fields, "Duration: "+t.Duration.String())
	for _, child := range t.Children {
		if r.opts.MaxDepth > 0 && depth >= r.opts.MaxDepth {
			r.line(fields, fmt.Sprintf("[child run %s omitted: depth limit %d]", child.RunID, r.opts.MaxDepth))
			continue
		}
		r.run(child, fields, depth+1)
	}
}

func (r *renderer) totals(t Totals, indent string) {
	r.line(indent, "Totals:")
	fields := indent + "  "
	r.line(fields, fmt.Sprintf("Model calls: %d (adopted: %d)", t.ModelCalls, t.AdoptedModelCalls))
	r.encoded(fields, "Model calls by class", t.ModelCallsByClass)
	r.encoded(fields, "Tokens by model", t.TokensByModel)
	r.line(fields, fmt.Sprintf("Tool calls: %d (failed: %d, repeated: %d)", t.ToolCalls, t.FailedToolCalls, t.RepeatedToolCalls))
	r.line(fields, "Wall time: "+t.WallTime.String())
}

func (r *renderer) line(indent, text string) {
	for i, line := range strings.Split(text, "\n") {
		prefix := indent
		if r.opts.Format == FormatMarkdown {
			if i == 0 {
				prefix += "- "
			} else {
				prefix += "  "
			}
		}
		r.out.WriteString(strings.TrimRight(prefix+line, " \t\r"))
		r.out.WriteByte('\n')
	}
}

func (r *renderer) encoded(indent, label string, value any) {
	data, err := callrecord.Encode(value)
	if err != nil {
		r.line(indent, label+": [invalid value: "+err.Error()+"]")
		return
	}
	r.value(indent, label, data)
}
