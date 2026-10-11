package index

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// StructuralSize measures the existing template projection with writer-owned
// parameter text. FinalizeResults receives the last assistant's still-open IDs.
// This is deterministic and reads no external content; hashes only select the
// supplied parameters, including text too large for inline storage.
func StructuralSize(entry MessageEntry, texts []string, openIDs []string) (contextmeasure.MessageSize, error) {
	if entry.Structural == nil || len(texts) != len(entry.Structural.Text) {
		return contextmeasure.MessageSize{}, fmt.Errorf("structural measurement requires resolved text parameters")
	}
	values := make(map[runstore.Hash][]byte)
	for i, param := range entry.Structural.Text {
		if param.Hash != "" {
			values[param.Hash] = []byte(texts[i])
		}
	}
	renderer := Renderer{ctx: context.Background(), hidden: map[string]bool{}, source: func(_ context.Context, hash runstore.Hash) ([]byte, error) {
		data, ok := values[hash]
		if !ok {
			return nil, fmt.Errorf("structural measurement missing parameter %s", hash)
		}
		return data, nil
	}}
	if entry.Structural.Kind == FinalizeResults && len(openIDs) > 0 {
		var parts []model.Part
		for _, id := range openIDs {
			parts = append(parts, model.ToolUsePart{ID: id})
		}
		renderer.messages = []*model.Message{{Role: model.ConversationRoleAssistant, Parts: parts}}
	}
	if err := renderer.appendStructural(entry); err != nil {
		return contextmeasure.MessageSize{}, err
	}
	renderer.flushResults()
	if entry.Structural.Kind == FinalizeResults && len(openIDs) > 0 {
		renderer.messages = renderer.messages[1:]
	}
	return contextmeasure.MeasureMessages(renderer.messages)
}
