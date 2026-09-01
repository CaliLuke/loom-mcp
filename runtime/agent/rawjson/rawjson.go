// Package rawjson provides a safe raw JSON byte type for workflow boundaries.
//
// # Motivation
//
// Go's jsontext.Value implements json.Marshaler and treats a non-nil empty
// slice as invalid JSON, returning:
// "json: error calling MarshalJSON for type jsontext.Value: unexpected end of JSON input".
//
// In this runtime, raw JSON byte fields are intentionally used at workflow and
// activity boundaries (tool payloads/results, hook envelopes, server-data
// sidecars). A single accidental `jsontext.Value{}` or `[]byte{}` assignment
// can therefore crash workflow encoding.
//
// Message eliminates that failure mode by normalizing empty/whitespace payloads
// to JSON null during marshaling while still validating non-empty payloads.
package rawjson

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
)

// Message is an opaque JSON value encoded as bytes.
//
// Contract:
//   - Nil represents absence (preferred).
//   - Non-empty values must be valid RFC 7493 JSON: UTF-8 encoded, with valid
//     Unicode and unique names in every object.
//   - Empty/whitespace-only values are normalized to JSON null during marshaling
//     to avoid runtime encoding failures at workflow boundaries.
type Message jsontext.Value

// RawMessage returns the underlying value as jsontext.Value.
func (r Message) RawMessage() jsontext.Value {
	return jsontext.Value(r)
}

// MarshalJSON implements json.Marshaler.
//
// This method never returns an "unexpected end of JSON input" error for empty
// slices; empty/whitespace is encoded as JSON null.
func (r Message) MarshalJSON() ([]byte, error) {
	data := []byte(r)
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("null"), nil
	}
	if !jsontext.Value(data).IsValid() {
		return nil, fmt.Errorf("rawjson: invalid JSON")
	}
	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
//
// The decoder validates non-null JSON and normalizes null to nil.
func (r *Message) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*r = nil
		return nil
	}
	if !jsontext.Value(trimmed).IsValid() {
		return fmt.Errorf("rawjson: invalid JSON")
	}
	out := make([]byte, len(trimmed))
	copy(out, trimmed)
	*r = Message(out)
	return nil
}
