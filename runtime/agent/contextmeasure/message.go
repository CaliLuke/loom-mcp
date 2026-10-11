package contextmeasure

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"

// MessageSize is runtime-computed index metadata. Bytes counts model-facing
// text; NonText counts estimated non-text tokens at K10's default image cost.
// Role is the last non-nil message's role for a transcript slice.
type MessageSize struct {
	Bytes   int
	NonText int
	Role    model.ConversationRole
}

// MeasureMessages uses the same part-size rules as request measurement, without
// counting metadata, binary encodings or message envelopes.
func MeasureMessages(messages []*model.Message) (MessageSize, error) {
	var size MessageSize
	for _, message := range messages {
		if message == nil {
			continue
		}
		size.Role = message.Role
		for _, part := range message.Parts {
			text, nontext, err := partSize(part, 0)
			if err != nil {
				return MessageSize{}, err
			}
			size.Bytes = addSize(size.Bytes, text)
			size.NonText = addSize(size.NonText, nontext)
		}
	}
	return size, nil
}
