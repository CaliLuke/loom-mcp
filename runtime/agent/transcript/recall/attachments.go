package recall

import (
	"context"
	"encoding/json/v2"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// AttachmentInfo identifies an original message part without exposing its bytes.
type AttachmentInfo struct {
	Message string `json:"message,omitzero"`
	Run     string `json:"run,omitzero"`
	Part    int    `json:"part"`
	Kind    string `json:"kind"`
	Format  string `json:"format"`
	Bytes   int    `json:"bytes"`
	Tokens  int    `json:"tokens"`
}

const reasonTooLarge = "too_large"

func (e *eligibleText) addAttachment(number int, part model.ToolResultContentPart) {
	info := AttachmentInfo{Part: number, Tokens: 1500}
	switch p := part.(type) {
	case model.ImagePart:
		info.Kind, info.Format, info.Bytes = "image", string(p.Format), len(p.Bytes)
	case model.DocumentPart:
		info.Kind, info.Format, info.Bytes = "document", string(p.Format), len(p.Bytes)
		if p.URI == "" {
			info.Tokens = (len(p.Bytes) + 1) / 2
		}
	}
	if len(e.attachments) == 0 {
		e.format, e.bytes = info.Format, info.Bytes
	}
	e.attachments = append(e.attachments, info)
	e.parts = append(e.parts, part)
}

// attachment follows only an eligible user message hash, never a result's parts.
func (r *callReader) attachment(ctx context.Context, runID, scope string, entry index.MessageEntry, number int) (Response, error) {
	if entry.Kind != index.Message {
		return Response{Code: CodeNotFound}, nil
	}
	data, err := r.handler.Store.Get(ctx, scope, entry.Hash)
	if err != nil {
		return Response{}, err
	}
	var message model.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return Response{}, err
	}
	if message.Role != model.ConversationRoleUser {
		return Response{Code: CodeNotFound}, nil
	}
	text := messageText([]*model.Message{&message})
	for i, info := range text.attachments {
		if info.Part != number {
			continue
		}
		part := text.parts[i]
		reason, limit, unit := r.req.attachmentRejection(part, info)
		if reason != "" {
			return Response{Code: CodeUnsupportedPart, Format: info.Format, Bytes: info.Bytes, Reason: reason, Limit: limit, LimitUnit: unit}, nil
		}

		info.Message = strconv.FormatUint(uint64(entry.ID), 10)
		info.Run = runID
		return Response{Attachment: &info, Parts: []model.ToolResultContentPart{part}}, nil
	}
	return Response{Code: CodeNotFound}, nil
}

// attachmentRejection enforces both the provider and workflow-stamped ceilings.
func (r Request) attachmentRejection(part model.ToolResultContentPart, info AttachmentInfo) (string, int, string) {
	accepted, reason := r.Parts.Accepts(part)
	if reason == reasonTooLarge {
		limit := r.Parts.MaxImageBytes
		if info.Kind == "document" {
			limit = r.Parts.MaxDocumentBytes
		}
		return reason, limit, "bytes"
	}
	if accepted && info.Tokens > r.PartTokens {
		return reasonTooLarge, r.PartTokens, "tokens"
	}
	if reason == "format" {
		reason = "provider"
	}
	return reason, 0, ""
}
