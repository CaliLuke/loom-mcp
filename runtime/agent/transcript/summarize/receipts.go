package summarize

import (
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// AnnotateReceipts copies K12's lines and attaches exact inclusive byte ranges
// and non-text annotations. It never mutates previous immutable receipts.
func AnnotateReceipts(lines []index.ReceiptLine, plan Plan) ([]index.ReceiptLine, error) {
	result := slices.Clone(lines)
	for i := range result {
		result[i].Unsummarised = slices.Clone(result[i].Unsummarised)
	}
	for _, span := range plan.Unsummarised {
		if span.Start < 0 || span.End <= span.Start {
			return nil, fmt.Errorf("summary invalid byte range")
		}
		matched := false
		for i := range result {
			if span.IsResult && span.ToolUseID != "" && result[i].Kind == "call" && result[i].ToolUseID == span.ToolUseID {
				result[i].Unsummarised = append(result[i].Unsummarised, index.ByteRange{From: span.Start, Through: span.End - 1})
				matched = true
				break
			}
		}
		if !matched {
			result = append(result, index.ReceiptLine{Kind: "part", From: span.MessageID, Through: span.MessageID, MessageID: span.MessageID, Part: span.Part, ToolUseID: span.ToolUseID, Bytes: span.End, Text: "not summarised: text", Unsummarised: []index.ByteRange{{From: span.Start, Through: span.End - 1}}})
		}
	}
	for _, part := range plan.Annotations {
		result = append(result, index.ReceiptLine{Kind: "part", From: part.MessageID, Through: part.MessageID, MessageID: part.MessageID, Part: part.Part, Format: part.Format, Bytes: part.Bytes, Text: part.String()})
	}
	return result, nil
}
