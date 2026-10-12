package fake

import (
	"encoding/json/v2"
	"strings"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// RequestText flattens text and structured results, preserving request order.
func RequestText(req *model.Request) (string, error) {
	var out strings.Builder
	for _, m := range req.Messages {
		if m == nil {
			continue
		}
		for _, p := range m.Parts {
			if err := appendPartText(&out, p); err != nil {
				return "", err
			}
		}
	}
	return out.String(), nil
}

// appendPartText decodes only bytes received in a model request, including recalled attachments.
func appendPartText(out *strings.Builder, part model.Part) error {
	switch v := part.(type) {
	case model.TextPart:
		out.WriteString(v.Text)
	case model.ImagePart:
		token, err := env.AssetToken("png", v.Bytes)
		if err != nil {
			return err
		}
		out.WriteString("image = " + token)
	case model.DocumentPart:
		token, err := env.AssetToken("pdf", v.Bytes)
		if err != nil {
			return err
		}
		out.WriteString("pdf = " + token)
	case model.ToolResultPart:
		if s, ok := v.Content.(string); ok {
			out.WriteString(s)
		} else {
			b, err := json.Marshal(v.Content)
			if err != nil {
				return err
			}
			out.Write(b)
		}
		for _, nested := range v.Parts {
			out.WriteByte('\n')
			if err := appendPartText(out, nested); err != nil {
				return err
			}
		}
	}
	out.WriteByte('\n')
	return nil
}
