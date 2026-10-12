package fake

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Summariser retains planted facts in order within the declared output budget.
type Summariser struct{ Faults Faults }

// RejectOnTrigger decorates offline and live summarisers with deterministic rejection.
type RejectOnTrigger struct{ Client model.Client }

const summaryStart = "# Goal\n# Decisions\n# Facts\n"
const summaryEnd = "\n# Progress\n# Open questions\n# Next\n"

var factLine = regexp.MustCompile(`[a-z][a-z_0-9]* = [A-Z]{3}-[0-9A-Z]{5}`)
var budgetPattern = regexp.MustCompile(`(?i)(?:budget|maximum|at most|within)[^0-9]{0,30}([0-9]+)`)

// Reject preserves a client's optional capabilities while adding the rejection trigger.
func Reject(client model.Client) model.Client {
	var counter model.TokenCounter
	if value, ok := client.(model.TokenCounter); ok {
		counter = value
	}
	var info model.ContextInfo
	if value, ok := client.(model.ContextInfo); ok {
		info = value
	}
	var parts model.ToolResultPartsInfo
	if value, ok := client.(model.ToolResultPartsInfo); ok {
		parts = value
	}
	return model.WithCapabilitySet(RejectOnTrigger{Client: client}, model.Capabilities{Counter: counter, Info: info, ToolResultParts: parts})
}

// Complete returns the evidence lines that fit, or drops them under a fault.
func (s *Summariser) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text, err := RequestText(req)
	if err != nil {
		return nil, err
	}
	limit := 1200
	if match := budgetPattern.FindStringSubmatch(text); len(match) > 1 {
		n, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, err
		}
		limit = min(limit, n*4)
	}
	if limit < len(summaryStart)+len(summaryEnd) {
		return nil, fmt.Errorf("summary budget cannot fit required headings")
	}
	var lines []string
	size := len(summaryStart) + len(summaryEnd)
	if !s.Faults.DropFacts {
		for _, line := range factLine.FindAllString(text, -1) {
			if size+len(line)+1 > limit {
				break
			}
			lines = append(lines, line)
			size += len(line) + 1
		}
	}
	return response(req, summaryStart+strings.Join(lines, "\n")+summaryEnd, nil)
}

// Stream is unsupported by the deterministic summariser.
func (s *Summariser) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return nil, model.ErrStreamingUnsupported
}

// Complete intercepts only the DSL rejection marker.
func (r RejectOnTrigger) Complete(ctx context.Context, req *model.Request) (*model.Response, error) {
	text, err := RequestText(req)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, "[[force-summary-rejection]]") {
		return response(req, summaryStart+"Use recall_context for everything."+summaryEnd, nil)
	}
	return r.Client.Complete(ctx, req)
}

// Stream delegates when no forced rejection is requested.
func (r RejectOnTrigger) Stream(ctx context.Context, req *model.Request) (model.ValidatedStreamer, error) {
	text, err := RequestText(req)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(text, "[[force-summary-rejection]]") {
		return r.Client.Stream(ctx, req)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := r.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	contract, err := model.NewRequestContract(req)
	if err != nil {
		return nil, err
	}
	return contract.ValidateStream(&rejectionStream{response: response, chunks: []model.Chunk{model.TextChunk{Message: response.Content[0]}, model.UsageChunk{Usage: response.Usage}, model.StopChunk{Reason: response.StopReason}}})
}
