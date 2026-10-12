package summarize

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type chunkPlanner struct {
	options ChunkOptions
	plan    Plan
	current Chunk
	ceiling int
}

// PlanChunks uses only recorded limits, the uncalibrated 1/3 table rate, and
// integer arithmetic. Every request reserves a full running summary plus 10% of
// the input budget. Result caps apply to raw eligible bytes, not quoted framing.
// Out <= (window-Out)/4 uses floor(window/5); when window mod 5 is 4 this is
// conservatively one token below floor(Bs/4), which has no exact integer solution.
func PlanChunks(evidence Evidence, options ChunkOptions) (Plan, error) {
	if options.MaxSummaryChunks == 0 {
		options.MaxSummaryChunks = MaxSummaryChunks
	}
	if options.Limits.ContextWindow <= 0 {
		return Plan{}, &Rejection{Kind: "summariser_limits_unknown"}
	}
	out := min(options.MaxSummaryTokens, options.Limits.ContextWindow/5)
	if out <= 0 {
		return Plan{}, fmt.Errorf("summarize: summary output budget must be positive")
	}
	budget, known := contextmeasure.Budget(options.Limits, out)
	if !known {
		return Plan{}, &Rejection{Kind: "summariser_limits_unknown"}
	}
	margin := budget / 10
	if budget%10 != 0 {
		margin++
	}
	p := chunkPlanner{options: options, plan: Plan{Budget: budget, OutputTokens: out, Annotations: slices.Clone(evidence.Annotations)}, current: Chunk{OutputTokens: out}, ceiling: budget - margin}
	for _, unit := range evidence.Units {
		if !utf8.ValidString(unit.Text) {
			return Plan{}, fmt.Errorf("summarize: message %d part %d is not UTF-8", unit.MessageID, unit.Part)
		}
		if err := p.appendUnit(unit); err != nil {
			return Plan{}, err
		}
	}
	p.flush()
	return p.plan, nil
}

// runeEnd returns the largest valid rune boundary at or before end.
func runeEnd(text string, end int) int {
	end = min(max(0, end), len(text))
	for end > 0 && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return end
}

func byteRange(unit Unit, start int) ByteRange {
	return ByteRange{MessageID: unit.MessageID, Part: unit.Part, ToolUseID: unit.ToolUseID, Start: start, End: len(unit.Text), IsResult: unit.IsResult}
}

func (p *chunkPlanner) appendUnit(unit Unit) error {
	if unit.MessageID == index.MessageID(0) {
		return fmt.Errorf("summarize: unit requires a message ID")
	}
	if unit.Attachment != nil {
		return p.appendAttachment(unit)
	}
	end := len(unit.Text)
	if unit.IsResult {
		resultTokens := p.options.MaxSummarisedResultTokens
		if resultTokens == 0 {
			resultTokens = p.plan.Budget / 2
		}
		end = runeEnd(unit.Text, min(end, tokenBytes(resultTokens)))
	}
	start := 0
	for start < end && len(p.plan.Chunks) < p.options.MaxSummaryChunks {
		next, err := p.fittingEnd(unit, start, end)
		if err != nil {
			return err
		}
		if next == start {
			if len(p.current.Pieces) == 0 {
				break
			}
			p.flush()
			continue
		}
		p.current.Pieces = append(p.current.Pieces, Piece{Unit: unit, Start: start, End: next})
		start = next
	}
	if start < len(unit.Text) {
		p.plan.Unsummarised = append(p.plan.Unsummarised, byteRange(unit, start))
	}
	return nil
}

func (p *chunkPlanner) appendAttachment(unit Unit) error {
	piece := Piece{Unit: unit, End: len(unit.Text)}
	fits, err := p.fits(piece)
	if err != nil {
		return err
	}
	if !fits && len(p.current.Pieces) > 0 {
		p.flush()
		fits, err = p.fits(piece)
		if err != nil {
			return err
		}
	}
	if fits && len(p.plan.Chunks) < p.options.MaxSummaryChunks {
		p.current.Pieces = append(p.current.Pieces, piece)
		return nil
	}
	annotation, err := partAnnotation(unit)
	if err != nil {
		return err
	}
	p.plan.Annotations = append(p.plan.Annotations, annotation)
	if len(unit.Text) > 0 {
		p.plan.Unsummarised = append(p.plan.Unsummarised, byteRange(unit, 0))
	}
	return nil
}

// fittingEnd binary-searches rendered request size, then aligns the final cut.
func (p *chunkPlanner) fittingEnd(unit Unit, start, end int) (int, error) {
	low, high := start, end
	for low < high {
		mid := low + (high-low)/2 + 1
		cut := runeEnd(unit.Text, mid)
		fits, err := p.fits(Piece{Unit: unit, Start: start, End: cut})
		if err != nil {
			return 0, err
		}
		if fits {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return runeEnd(unit.Text, low), nil
}

func (p *chunkPlanner) fits(piece Piece) (bool, error) {
	if len(p.plan.Chunks) >= p.options.MaxSummaryChunks {
		return false, nil
	}
	chunk := p.current
	chunk.Pieces = append(slices.Clone(chunk.Pieces), piece)
	prompt := p.options.Prompt
	if p.options.PromptFor != nil {
		var err error
		prompt, err = p.options.PromptFor(strings.Repeat("x", tokenBytes(p.plan.OutputTokens)), chunk)
		if err != nil {
			return false, err
		}
	}
	request := RenderChunk(prompt, "", chunk)
	facts, err := contextmeasure.Measure(request, p.options.Limits, nil)
	if err != nil {
		return false, err
	}
	estimate := contextmeasure.Estimate(contextmeasure.Rate{}, tokenBytes(p.plan.OutputTokens), facts.TextBytes, facts.NonText)
	return estimate <= p.ceiling, nil
}

func (p *chunkPlanner) flush() {
	if len(p.current.Pieces) == 0 {
		return
	}
	p.plan.Chunks = append(p.plan.Chunks, p.current)
	p.current = Chunk{OutputTokens: p.plan.OutputTokens}
}
