// Package recall defines input and paging contracts and an activity-side
// evidence handler. Workflows own allowance charging and dispatch.
package recall

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Kind selects the allowance and operation even for malformed known inputs.
	Kind string
	// Input contains exactly one operation. ParseInput preserves a recognized
	// Kind on validation failure; callers charge discovery when Kind is empty.
	Input struct {
		Kind Kind       `json:"-"`
		Read *ReadInput `json:"read,omitempty"`
		List *ListInput `json:"list,omitempty"`
		Find *FindInput `json:"find,omitempty"`
	}
	// ReadInput addresses eligible content. Offset is a nonnegative byte offset.
	ReadInput struct {
		ID     string `json:"id"`
		Run    string `json:"run,omitempty"`
		Part   Part   `json:"part"`
		Offset int    `json:"offset"`
		Index  int    `json:"index,omitzero"`
	}
	// ListInput selects either a collapsed handle or an inclusive message range.
	ListInput struct {
		Handle  string          `json:"handle,omitempty"`
		From    index.MessageID `json:"from,omitempty"`
		Through index.MessageID `json:"through,omitempty"`
		Run     string          `json:"run,omitempty"`
		Cursor  string          `json:"cursor,omitempty"`
	}
	// FindInput searches for a literal, case-sensitive UTF-8 string.
	FindInput struct {
		Text   string `json:"text"`
		Cursor string `json:"cursor,omitempty"`
	}
	// Part selects the model-visible result or original tool arguments.
	Part string
)

const (
	// Read consumes the read allowance.
	Read Kind = "read"
	// List consumes the discovery allowance.
	List Kind = "list"
	// Find consumes the discovery allowance.
	Find Kind = "find"
	// Result selects eligible result, message or summary text.
	Result Part = "result"
	// Arguments selects a tool call's original arguments.
	Arguments Part = "arguments"
	// Attachment selects one non-text user-message part.
	Attachment Part = "attachment"
)

// ParseInput validates the exactly-one operation envelope without resolving IDs
// or cursors. A malformed recognized operation keeps its Kind for charging.
func ParseInput(data []byte) (Input, error) {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 1 {
		return Input{}, invalidInput()
	}
	in := Input{}
	for key := range fields {
		switch Kind(key) {
		case Read, List, Find:
			in.Kind = Kind(key)
		default:
			return in, invalidInput()
		}
	}
	var err error
	switch in.Kind {
	case Read:
		in.Read = new(ReadInput)
		err = parseRead(fields[string(Read)], in.Read)
	case List:
		in.List = new(ListInput)
		err = parseList(fields[string(List)], in.List)
	case Find:
		in.Find = new(FindInput)
		_, err = decodeObject(fields[string(Find)], in.Find, "text")
		if err == nil && in.Find.Text == "" {
			err = invalidInput()
		}
	}
	if err != nil {
		return Input{Kind: in.Kind}, err
	}
	return in, nil
}

func parseRead(data []byte, in *ReadInput) error {
	fields, err := decodeObject(data, in, "id", "part", "offset")
	if err != nil {
		return err
	}
	if in.ID == "" || (in.Part != Result && in.Part != Arguments && in.Part != Attachment) || in.Offset < 0 {
		return invalidInput()
	}
	if in.Part == Attachment {
		if in.Index <= 0 || in.Offset != 0 {
			return invalidInput()
		}
	} else if _, present := fields["index"]; present {
		return invalidInput()
	}
	return nil
}

func parseList(data []byte, in *ListInput) error {
	fields, err := decodeObject(data, in)
	if err != nil {
		return err
	}
	// Presence, rather than zero values, rejects mixed or empty selectors.
	if len(fields["handle"]) > 0 {
		if in.Handle == "" || len(fields["from"]) > 0 || len(fields["through"]) > 0 || len(fields["run"]) > 0 {
			return invalidInput()
		}
		return nil
	}
	if len(fields["from"]) == 0 || len(fields["through"]) == 0 || in.From == 0 || in.Through < in.From {
		return invalidInput()
	}
	return nil
}

// decodeObject rejects null fields, absent required fields and unknown names.
// Parser diagnostics stay private because they can quote caller-supplied data.
func decodeObject(data []byte, target any, required ...string) (map[string]jsontext.Value, error) {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, invalidInput()
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, invalidInput()
		}
	}
	for _, key := range required {
		if len(fields[key]) == 0 {
			return nil, invalidInput()
		}
	}
	if err := json.Unmarshal(data, target, json.RejectUnknownMembers(true)); err != nil {
		return nil, invalidInput()
	}
	return fields, nil
}
