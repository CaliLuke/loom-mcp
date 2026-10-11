package contextmeasure

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Measure returns pre-send facts; callers supply the resolved model and call key.
// Binary uploads contribute only NonText; Meta, cache points, thinking signatures
// and citation metadata are not text. Document names/context and tool names/IDs
// are provider-facing text. JSON costs measure the payload, not its wire envelope.
func Measure(req *model.Request, limits model.ContextLimits, view []*model.Message) (RequestFacts, error) {
	if req == nil {
		return RequestFacts{}, errors.New("contextmeasure: nil request")
	}
	facts := RequestFacts{Model: req.Model, ContextWindow: limits.ContextWindow, OutputReserve: OutputReserve(req, limits)}
	matches, err := viewMatches(req, view)
	if err != nil {
		return RequestFacts{}, err
	}
	facts.ViewBearing = len(matches) > 0
	for _, message := range req.Messages {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			text, nontext, err := partSize(part, limits.ImageTokens)
			if err != nil {
				return RequestFacts{}, err
			}
			facts.TextBytes = addSize(facts.TextBytes, text)
			facts.NonText = addSize(facts.NonText, nontext)
			if matches[message] {
				facts.ViewTextBytes = addSize(facts.ViewTextBytes, text)
				facts.ViewNonText = addSize(facts.ViewNonText, nontext)
			}
			if message.Role == model.ConversationRoleSystem {
				facts.FixedBytes = addSize(facts.FixedBytes, text)
			}
		}
	}
	for _, tool := range req.Tools {
		if tool == nil {
			continue
		}
		schema, err := jsonSize(tool.InputSchema)
		if err != nil {
			return RequestFacts{}, err
		}
		size := addSize(addSize(len(tool.Name), len(tool.Description)), schema)
		facts.FixedBytes = addSize(facts.FixedBytes, size)
		facts.TextBytes = addSize(facts.TextBytes, size)
	}
	return facts, nil
}

func partSize(part model.Part, imageTokens int) (text, nontext int, err error) {
	part, err = normalizePart(part)
	if err != nil {
		return 0, 0, err
	}
	if imageTokens <= 0 {
		imageTokens = 1500
	}
	switch p := part.(type) {
	case model.TextPart:
		return len(p.Text), 0, nil
	case model.CitationsPart:
		return len(p.Text), 0, nil
	case model.ThinkingPart:
		return addSize(len(p.Text), len(p.Redacted)), 0, nil
	case model.ImagePart:
		return 0, imageTokens, nil
	case model.DocumentPart:
		text = addSize(addSize(len(p.Name), len(p.Context)), len(p.Text))
		for _, chunk := range p.Chunks {
			text = addSize(text, len(chunk))
		}
		if len(p.Bytes) > 0 {
			nontext = len(p.Bytes)/2 + len(p.Bytes)%2
		} else if p.URI != "" {
			nontext = imageTokens
		}
		return text, nontext, nil
	case model.ToolUsePart:
		size, err := jsonSize(p.Input)
		return addSize(addSize(len(p.ID), len(p.Name)), size), 0, err
	case model.ToolResultPart:
		size, err := jsonSize(p.Content)
		return addSize(len(p.ToolUseID), size), 0, err
	case model.CacheCheckpointPart:
		return 0, 0, nil
	default:
		return 0, 0, fmt.Errorf("contextmeasure: unsupported part %T", part)
	}
}

// normalizePart treats pointer and value parts identically and rejects nils.
// model.Part is sealed: its concrete implementations are structs or their pointers.
func normalizePart(part model.Part) (model.Part, error) {
	if part == nil {
		return nil, errors.New("contextmeasure: nil part")
	}
	value := reflect.ValueOf(part)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, fmt.Errorf("contextmeasure: nil part %T", part)
		}
		return value.Elem().Interface().(model.Part), nil
	}
	return part, nil
}

func jsonSize(value any) (int, error) {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return 0, fmt.Errorf("contextmeasure: JSON payload: %w", err)
	}
	return len(data), nil
}
