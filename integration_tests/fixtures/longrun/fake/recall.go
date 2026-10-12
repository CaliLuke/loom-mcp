package fake

import (
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

func lastRecall(req *model.Request) (recall.Input, recall.Response, bool, error) {
	uses := map[string]model.ToolUsePart{}
	var latest *model.ToolResultPart
	for _, m := range req.Messages {
		for _, p := range m.Parts {
			switch v := p.(type) {
			case model.ToolUsePart:
				uses[v.ID] = v
			case model.ToolResultPart:
				latest = &v
			}
		}
	}
	if latest == nil {
		return recall.Input{}, recall.Response{}, false, nil
	}
	use, ok := uses[latest.ToolUseID]
	if !ok || use.Name != string(tools.RecallContext) {
		return recall.Input{}, recall.Response{}, false, nil
	}
	var input recall.Input
	var result recall.Response
	data, err := json.Marshal(use.Input)
	if err != nil {
		return input, result, false, err
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return input, result, false, err
	}
	data, err = json.Marshal(latest.Content)
	if err != nil {
		return input, result, false, err
	}
	if text, ok := latest.Content.(string); ok {
		data = []byte(text)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return input, result, false, fmt.Errorf("decode latest recall result: %w", err)
	}
	return input, result, true, nil
}

// nextRecallRead follows discovery with a text read, then uses only returned
// attachment indices to retrieve missing PNG/PDF evidence from its original message.
func nextRecallRead(req *model.Request, keys []string, text string) (recall.Input, bool, error) {
	input, result, found, err := lastRecall(req)
	if err != nil || !found || result.Code != "" {
		return recall.Input{}, false, err
	}
	if input.Find != nil && len(result.Matches) > 0 {
		hit := result.Matches[0]
		offset := hit.Offset
		if input.Find.Text == "image = " || input.Find.Text == "pdf = " {
			offset = 0 // Attachment descriptors occur only on the first text page.
		}
		return recall.Input{Read: &recall.ReadInput{ID: hit.ID, Run: hit.Run, Part: hit.Part, Offset: offset}}, true, nil
	}
	if input.Read != nil && input.Read.Part == recall.Result {
		for _, attachment := range result.Attachments {
			for _, key := range keys {
				matches := (key == "image" && attachment.Kind == "image" && attachment.Format == "png") || (key == "pdf" && attachment.Kind == "document" && attachment.Format == "pdf")
				if matches && fact(text, key, false) == "" {
					return recall.Input{Read: &recall.ReadInput{ID: input.Read.ID, Run: input.Read.Run, Part: recall.Attachment, Index: attachment.Part}}, true, nil
				}
			}
		}
	}
	return recall.Input{}, false, nil
}
func recallStopped(req *model.Request) bool {
	input, result, found, err := lastRecall(req)
	return err != nil || (found && (result.Code != "" || (input.Find != nil && len(result.Matches) == 0)))
}
