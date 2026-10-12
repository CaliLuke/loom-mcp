package summaryeval

import (
	"bytes"
	"context"
	"strings"
	"text/template"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/summarize"
)

type promptData struct {
	Previous, Evicted, Instructions string
	Budget                          int
}

func evidence(c Cut, in *agRuntime.SummarizeContextInput) (summarize.Evidence, error) {
	previous := ""
	pos := 0
	if c.Prev != nil {
		cp, ok := findCheckpoint(c.run, *c.Prev)
		if ok {
			pos = cp.SummaryPos
			if cp.Summary != nil {
				previous = string(c.record.Content[*cp.Summary])
			}
		}
	}
	return summarize.RenderInput(summarize.Input{Entries: c.run.Entries, Contents: c.record.Content, From: c.From, To: c.To, Previous: previous, CurrentPos: in.ViewRef.ChainPos, SummaryPos: pos, AcceptsImages: in.ContextModel.AcceptsImages, AcceptsDocuments: in.ContextModel.AcceptsDocuments})
}

func plan(ctx context.Context, c Cut, in *agRuntime.SummarizeContextInput, m Model) (int, int, error) {
	ev, err := evidence(c, in)
	if err != nil {
		return 0, 0, err
	}
	var limits model.ContextLimits
	if info, ok := m.Client.(model.ContextInfo); ok {
		limits, _, _ = info.ContextLimits(ctx, &model.Request{ModelClass: in.ContextModel.Class})
	}
	if limits.ContextWindow <= 0 {
		return 0, 0, nil
	}
	render, err := promptRenderer(c, in)
	if err != nil {
		return 0, 0, err
	}
	p, err := summarize.PlanChunks(ev, summarize.ChunkOptions{MaxSummaryChunks: in.ContextSettings.MaxSummaryChunks, MaxSummarisedResultTokens: in.ContextSettings.MaxSummarisedResultTokens, Limits: limits, MaxSummaryTokens: in.Budget.MaxSummaryTokens, PromptFor: render})
	if err != nil {
		return 0, 0, err
	}
	tokens := 0
	for _, chunk := range p.Chunks {
		text, err := render(strings.Repeat("x", p.OutputTokens*3), chunk)
		if err != nil {
			return 0, 0, err
		}
		facts, err := contextmeasure.Measure(summarize.RenderChunk(text, "", chunk), limits, nil)
		if err != nil {
			return 0, 0, err
		}
		tokens += contextmeasure.Estimate(contextmeasure.Rate{}, 0, facts.TextBytes, facts.NonText)
	}
	return len(p.Chunks), tokens, nil
}

func promptRenderer(c Cut, in *agRuntime.SummarizeContextInput) (func(string, summarize.Chunk) (string, error), error) {
	text := string(c.record.Content[in.ContextSettings.Prompt.Template])
	t, err := template.New(string(in.PromptID)).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, err
	}
	render := func(previous string, chunk summarize.Chunk) (string, error) {
		previousReq := summarize.RenderChunk("", previous, summarize.Chunk{OutputTokens: chunk.OutputTokens})
		data := messageText(previousReq.Messages[1])
		_, data, _ = strings.Cut(data, "Previous summary:\n")
		data, _, _ = strings.Cut(data, "\nTranscript (quoted data):")
		evicted := messageText(summarize.RenderChunk("", "", chunk).Messages[1])
		_, evicted, _ = strings.Cut(evicted, "Transcript (quoted data):\n")
		var out bytes.Buffer
		err := t.Execute(&out, promptData{Previous: data, Evicted: evicted, Instructions: in.Settings.Instructions, Budget: chunk.OutputTokens})
		return out.String(), err
	}
	return render, nil
}
