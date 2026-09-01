// Package model defines JSON helpers for marshaling and unmarshaling provider
// message parts. This file focuses on decoding messages and discriminating
// concrete part types based on the Kind field.
package model

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
)

const (
	partKindThinking        = "thinking"
	partKindText            = "text"
	partKindImage           = "image"
	partKindDocument        = "document"
	partKindCitations       = "citations"
	partKindToolUse         = "tool_use"
	partKindToolResult      = "tool_result"
	partKindCacheCheckpoint = "cache_checkpoint"
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
		}, json.FormatNilMapAsNull(true), json.FormatNilSliceAsNull(true))
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
	}, json.FormatNilMapAsNull(true), json.FormatNilSliceAsNull(true))
}

// UnmarshalJSON decodes a Message while materializing concrete Part
// implementations stored in the Parts slice.
func (m *Message) UnmarshalJSON(data []byte) error {
	type alias struct {
		Role  ConversationRole `json:"Role"` //nolint:tagliatelle
		Parts []jsontext.Value
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
	normalized, err := normalizeMessagePart(p)
	if err != nil {
		return nil, err
	}
	switch v := normalized.(type) {
	case ThinkingPart:
		return encodeThinkingPart(v), nil
	case TextPart:
		return encodeTextPart(v), nil
	case ImagePart:
		if err := validateImagePart(v); err != nil {
			return nil, err
		}
		return encodeImagePart(v), nil
	case DocumentPart:
		if err := validateDocumentPart(v); err != nil {
			return nil, err
		}
		return encodeDocumentPart(v), nil
	case CitationsPart:
		return encodeCitationsPart(v), nil
	case ToolUsePart:
		if err := validateToolUsePart(v); err != nil {
			return nil, err
		}
		return encodeToolUsePart(v), nil
	case ToolResultPart:
		if err := validateToolResultPart(v); err != nil {
			return nil, err
		}
		return encodeToolResultPart(v), nil
	case CacheCheckpointPart:
		return encodeCacheCheckpointPart(), nil
	default:
		return nil, fmt.Errorf("unknown part type %T", normalized)
	}
}

func normalizeMessagePart(part Part) (Part, error) {
	switch v := part.(type) {
	case *ThinkingPart:
		return dereferencePart(v, "ThinkingPart")
	case *TextPart:
		return dereferencePart(v, "TextPart")
	case *ImagePart:
		return dereferencePart(v, "ImagePart")
	case *DocumentPart:
		return dereferencePart(v, "DocumentPart")
	case *CitationsPart:
		return dereferencePart(v, "CitationsPart")
	case *ToolUsePart:
		return dereferencePart(v, "ToolUsePart")
	case *ToolResultPart:
		return dereferencePart(v, "ToolResultPart")
	case *CacheCheckpointPart:
		return dereferencePart(v, "CacheCheckpointPart")
	default:
		return part, nil
	}
}

func dereferencePart[T Part](part *T, typeName string) (Part, error) {
	if part == nil {
		return nil, fmt.Errorf("nil %s", typeName)
	}
	return *part, nil
}

func encodeThinkingPart(v ThinkingPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ThinkingPart
	}{Kind: partKindThinking, ThinkingPart: v}
}

func encodeTextPart(v TextPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		TextPart
	}{Kind: partKindText, TextPart: v}
}

func encodeImagePart(v ImagePart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ImagePart
	}{Kind: partKindImage, ImagePart: v}
}

func encodeDocumentPart(v DocumentPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		DocumentPart
	}{Kind: partKindDocument, DocumentPart: v}
}

func encodeCitationsPart(v CitationsPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		CitationsPart
	}{Kind: partKindCitations, CitationsPart: v}
}

func encodeToolUsePart(v ToolUsePart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ToolUsePart
	}{Kind: partKindToolUse, ToolUsePart: v}
}

func encodeToolResultPart(v ToolResultPart) any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
		ToolResultPart
	}{Kind: partKindToolResult, ToolResultPart: v}
}

func encodeCacheCheckpointPart() any {
	return struct {
		Kind string `json:"Kind"` //nolint:tagliatelle // Kind discriminator is intentionally upper-cased for compatibility.
	}{Kind: partKindCacheCheckpoint}
}

func decodeMessagePart(raw jsontext.Value) (Part, error) {
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

func hasAnyKey(obj map[string]jsontext.Value, keys ...string) bool {
	for _, k := range keys {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

func decodePartObject(raw jsontext.Value) (map[string]jsontext.Value, error) {
	var obj map[string]jsontext.Value
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode part object: %w", err)
	}
	if len(obj) == 0 {
		return nil, errors.New("empty part payload")
	}
	return obj, nil
}

func decodeRawTextPart(raw jsontext.Value) (Part, bool) {
	var text *string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, false
	}
	if text == nil {
		return nil, false
	}
	return TextPart{Text: *text}, true
}

func decodePartByKind(raw jsontext.Value, obj map[string]jsontext.Value, kindRaw jsontext.Value) (Part, error) {
	var kind string
	if err := json.Unmarshal(kindRaw, &kind); err != nil {
		return nil, fmt.Errorf("decode Kind: %w", err)
	}
	switch kind {
	case partKindImage:
		return decodeImagePart(raw)
	case partKindDocument:
		return decodeDocumentPart(raw)
	case partKindThinking:
		return decodeThinkingPart(raw)
	case partKindCitations:
		return decodeCitationsPart(raw)
	case partKindToolResult:
		return decodeToolResultPart(raw)
	case partKindToolUse:
		return decodeToolUsePart(raw, obj)
	case partKindText:
		return decodeTextPart(raw)
	case partKindCacheCheckpoint:
		return CacheCheckpointPart{}, nil
	default:
		return nil, fmt.Errorf("unknown part kind %q", kind)
	}
}

func decodePartByShape(raw jsontext.Value, obj map[string]jsontext.Value) (Part, error) {
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

func decodeImagePart(raw jsontext.Value) (Part, error) {
	var img ImagePart
	if err := json.Unmarshal(raw, &img); err != nil {
		return nil, fmt.Errorf("decode ImagePart: %w", err)
	}
	if err := validateImagePart(img); err != nil {
		return nil, err
	}
	return img, nil
}

func validateImagePart(img ImagePart) error {
	if img.Format == "" {
		return errors.New("ImagePart requires Format")
	}
	if len(img.Bytes) == 0 {
		return errors.New("ImagePart requires Bytes")
	}
	return nil
}

func decodeDocumentPart(raw jsontext.Value) (Part, error) {
	var doc DocumentPart
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode DocumentPart: %w", err)
	}
	if err := validateDocumentPart(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func validateDocumentPart(doc DocumentPart) error {
	if doc.Name == "" {
		return errors.New("DocumentPart requires Name")
	}
	return validateDocumentSources(doc)
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

func decodeThinkingPart(raw jsontext.Value) (Part, error) {
	var thinking ThinkingPart
	if err := json.Unmarshal(raw, &thinking); err != nil {
		return nil, fmt.Errorf("decode ThinkingPart: %w", err)
	}
	return thinking, nil
}

func decodeCitationsPart(raw jsontext.Value) (Part, error) {
	var citations CitationsPart
	if err := json.Unmarshal(raw, &citations); err != nil {
		return nil, fmt.Errorf("decode CitationsPart: %w", err)
	}
	return citations, nil
}

func decodeToolResultPart(raw jsontext.Value) (Part, error) {
	var result ToolResultPart
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode ToolResultPart: %w", err)
	}
	if err := validateToolResultPart(result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateToolResultPart(result ToolResultPart) error {
	if result.ToolUseID == "" {
		return errors.New("ToolResultPart requires ToolUseID")
	}
	return nil
}

func decodeToolUsePart(raw jsontext.Value, obj map[string]jsontext.Value) (Part, error) {
	var use ToolUsePart
	if err := json.Unmarshal(raw, &use); err != nil {
		return nil, fmt.Errorf("decode ToolUsePart: %w", err)
	}
	if err := validateToolUsePart(use); err != nil {
		return nil, err
	}
	if err := applyToolUseArgsFallback(obj, &use); err != nil {
		return nil, err
	}
	return use, nil
}

func validateToolUsePart(use ToolUsePart) error {
	if use.Name == "" {
		return errors.New("ToolUsePart requires Name")
	}
	return nil
}

func applyToolUseArgsFallback(obj map[string]jsontext.Value, use *ToolUsePart) error {
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

func decodeTextPart(raw jsontext.Value) (Part, error) {
	var text TextPart
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, fmt.Errorf("decode TextPart: %w", err)
	}
	return text, nil
}

func hasKey(obj map[string]jsontext.Value, key string) bool {
	_, ok := obj[key]
	return ok
}
