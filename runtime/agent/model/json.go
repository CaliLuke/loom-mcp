// Package model defines JSON helpers for marshaling and unmarshaling provider
// message parts. This file focuses on decoding messages and discriminating
// concrete part types based on the Kind field.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// MarshalJSON encodes a Message while preserving the concrete Part types stored
// in Parts via an explicit Kind discriminator.
//
// This ensures round-trips through JSON do not lose type information when Parts
// are stored as an interface slice.
func (m Message) MarshalJSON() ([]byte, error) {
	type alias struct {
		Role  ConversationRole `json:"Role"`  //nolint:tagliatelle
		Parts []any            `json:"Parts"` //nolint:tagliatelle
		Meta  map[string]any   `json:"Meta"`  //nolint:tagliatelle
	}
	if len(m.Parts) == 0 {
		return json.Marshal(alias{
			Role:  m.Role,
			Parts: nil,
			Meta:  m.Meta,
		})
	}

	parts := make([]any, 0, len(m.Parts))
	for i, p := range m.Parts {
		enc, err := encodeMessagePart(p)
		if err != nil {
			return nil, fmt.Errorf("encode parts[%d]: %w", i, err)
		}
		parts = append(parts, enc)
	}

	return json.Marshal(alias{
		Role:  m.Role,
		Parts: parts,
		Meta:  m.Meta,
	})
}

// UnmarshalJSON decodes a Message while materializing concrete Part
// implementations stored in the Parts slice.
func (m *Message) UnmarshalJSON(data []byte) error {
	type alias struct {
		Role  ConversationRole `json:"Role"` //nolint:tagliatelle
		Parts []json.RawMessage
		Meta  map[string]any `json:"Meta"` //nolint:tagliatelle
	}
	var tmp alias
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	m.Role = tmp.Role
	m.Meta = tmp.Meta
	if len(tmp.Parts) == 0 {
		m.Parts = nil
		return nil
	}
	m.Parts = make([]Part, 0, len(tmp.Parts))
	for i, raw := range tmp.Parts {
		part, err := decodeMessagePart(raw)
		if err != nil {
			return fmt.Errorf("decode parts[%d]: %w", i, err)
		}
		m.Parts = append(m.Parts, part)
	}
	return nil
}

func encodeMessagePart(p Part) (any, error) {
	switch v := p.(type) {
	case ThinkingPart:
		return encodeThinkingPart(v), nil
	case TextPart:
		return encodeTextPart(v), nil
	case ImagePart:
		return encodeImagePart(v), nil
	case DocumentPart:
		return encodeDocumentPart(v), nil
	case CitationsPart:
		return encodeCitationsPart(v), nil
	case ToolUsePart:
		return encodeToolUsePart(v), nil
	case ToolResultPart:
		return encodeToolResultPart(v), nil
	case CacheCheckpointPart:
		return encodeCacheCheckpointPart(), nil
	default:
		return nil, fmt.Errorf("unknown part type %T", p)
	}
}

func encodeThinkingPart(v ThinkingPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ThinkingPart
	}{Kind: "thinking", ThinkingPart: v}
}

func encodeTextPart(v TextPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		TextPart
	}{Kind: "text", TextPart: v}
}

func encodeImagePart(v ImagePart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ImagePart
	}{Kind: "image", ImagePart: v}
}

func encodeDocumentPart(v DocumentPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		DocumentPart
	}{Kind: "document", DocumentPart: v}
}

func encodeCitationsPart(v CitationsPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		CitationsPart
	}{Kind: "citations", CitationsPart: v}
}

func encodeToolUsePart(v ToolUsePart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ToolUsePart
	}{Kind: "tool_use", ToolUsePart: v}
}

func encodeToolResultPart(v ToolResultPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ToolResultPart
	}{Kind: "tool_result", ToolResultPart: v}
}

func encodeCacheCheckpointPart() any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
	}{Kind: "cache_checkpoint"}
}

func decodeMessagePart(raw json.RawMessage) (Part, error) {
	obj, err := decodePartObject(raw)
	if err != nil {
		if text, ok := decodeRawTextPart(raw); ok {
			return text, nil
		}
		return nil, err
	}
	if kindRaw, ok := obj["Kind"]; ok {
		return decodePartByKind(raw, obj, kindRaw)
	}
	return decodePartByShape(raw, obj)
}

func hasAnyKey(obj map[string]json.RawMessage, keys ...string) bool {
	for _, k := range keys {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

func decodePartObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode part object: %w", err)
	}
	if len(obj) == 0 {
		return nil, errors.New("empty part payload")
	}
	return obj, nil
}

func decodeRawTextPart(raw json.RawMessage) (Part, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, false
	}
	return TextPart{Text: text}, true
}

func decodePartByKind(raw json.RawMessage, obj map[string]json.RawMessage, kindRaw json.RawMessage) (Part, error) {
	var kind string
	if err := json.Unmarshal(kindRaw, &kind); err != nil {
		return nil, fmt.Errorf("decode Kind: %w", err)
	}
	switch kind {
	case "image":
		return decodeImagePart(raw)
	case "document":
		return decodeDocumentPart(raw)
	case "thinking":
		return decodeThinkingPart(raw)
	case "citations":
		return decodeCitationsPart(raw)
	case "tool_result":
		return decodeToolResultPart(raw)
	case "tool_use":
		return decodeToolUsePart(raw, obj)
	case "text":
		return decodeTextPart(raw)
	case "cache_checkpoint":
		return CacheCheckpointPart{}, nil
	default:
		return nil, fmt.Errorf("unknown part kind %q", kind)
	}
}

func decodePartByShape(raw json.RawMessage, obj map[string]json.RawMessage) (Part, error) {
	switch {
	case hasAnyKey(obj, "Signature", "Redacted", "Index", "Final"):
		return decodeThinkingPart(raw)
	case hasKey(obj, "ToolUseID"):
		return decodeToolResultPart(raw)
	case hasKey(obj, "Name"):
		return decodeToolUsePart(raw, obj)
	case hasKey(obj, "Text"):
		return decodeTextPart(raw)
	default:
		return nil, errors.New("unknown part shape")
	}
}

func decodeImagePart(raw json.RawMessage) (Part, error) {
	var img ImagePart
	if err := json.Unmarshal(raw, &img); err != nil {
		return nil, fmt.Errorf("decode ImagePart: %w", err)
	}
	if img.Format == "" {
		return nil, errors.New("ImagePart requires Format")
	}
	if len(img.Bytes) == 0 {
		return nil, errors.New("ImagePart requires Bytes")
	}
	return img, nil
}

func decodeDocumentPart(raw json.RawMessage) (Part, error) {
	var doc DocumentPart
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode DocumentPart: %w", err)
	}
	if doc.Name == "" {
		return nil, errors.New("DocumentPart requires Name")
	}
	if err := validateDocumentSources(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func validateDocumentSources(doc DocumentPart) error {
	sourceCount := 0
	if len(doc.Bytes) > 0 {
		sourceCount++
	}
	if doc.Text != "" {
		sourceCount++
	}
	if len(doc.Chunks) > 0 {
		sourceCount++
	}
	if doc.URI != "" {
		sourceCount++
	}
	if sourceCount != 1 {
		return errors.New("DocumentPart requires exactly one of Bytes, Text, Chunks, or URI")
	}
	for i, chunk := range doc.Chunks {
		if chunk == "" {
			return fmt.Errorf("DocumentPart requires non-empty Chunks[%d]", i)
		}
	}
	return nil
}

func decodeThinkingPart(raw json.RawMessage) (Part, error) {
	var thinking ThinkingPart
	if err := json.Unmarshal(raw, &thinking); err != nil {
		return nil, fmt.Errorf("decode ThinkingPart: %w", err)
	}
	return thinking, nil
}

func decodeCitationsPart(raw json.RawMessage) (Part, error) {
	var citations CitationsPart
	if err := json.Unmarshal(raw, &citations); err != nil {
		return nil, fmt.Errorf("decode CitationsPart: %w", err)
	}
	return citations, nil
}

func decodeToolResultPart(raw json.RawMessage) (Part, error) {
	var result ToolResultPart
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode ToolResultPart: %w", err)
	}
	if result.ToolUseID == "" {
		return nil, errors.New("ToolResultPart requires ToolUseID")
	}
	return result, nil
}

func decodeToolUsePart(raw json.RawMessage, obj map[string]json.RawMessage) (Part, error) {
	var use ToolUsePart
	if err := json.Unmarshal(raw, &use); err != nil {
		return nil, fmt.Errorf("decode ToolUsePart: %w", err)
	}
	if use.Name == "" {
		return nil, errors.New("ToolUsePart requires Name")
	}
	if err := applyToolUseArgsFallback(obj, &use); err != nil {
		return nil, err
	}
	return use, nil
}

func applyToolUseArgsFallback(obj map[string]json.RawMessage, use *ToolUsePart) error {
	if use.Input != nil || hasKey(obj, "Input") {
		return nil
	}
	v, hasArgs := obj["Args"]
	if !hasArgs {
		return nil
	}
	var args any
	if err := json.Unmarshal(v, &args); err != nil {
		return fmt.Errorf("decode ToolUsePart Args: %w", err)
	}
	use.Input = args
	return nil
}

func decodeTextPart(raw json.RawMessage) (Part, error) {
	var text TextPart
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, fmt.Errorf("decode TextPart: %w", err)
	}
	return text, nil
}

func hasKey(obj map[string]json.RawMessage, key string) bool {
	_, ok := obj[key]
	return ok
}
