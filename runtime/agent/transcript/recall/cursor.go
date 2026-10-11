package recall

import (
	"encoding/base32"
	"encoding/json/v2"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Cursor is an untrusted continuation selector, never a capability. A handler
	// must re-resolve its selector through the current checkpoint chain on every
	// call and bind it to the requested operation before using Position.
	Cursor struct {
		Kind     Kind        `json:"kind"`
		Handle   string      `json:"handle,omitempty"`
		Range    *Range      `json:"range,omitempty"`
		Scan     string      `json:"scan,omitempty"`
		Position int         `json:"position"`
		Anchor   *FindAnchor `json:"anchor,omitempty"`
	}
	// FindAnchor identifies an immutable entry and its text/call chunk in scan order.
	// Position in the enclosing cursor is a byte offset within this chunk.
	FindAnchor struct {
		Run   string          `json:"run"`
		Entry index.MessageID `json:"entry"`
		Chunk int             `json:"chunk"`
	}
	// Range is the inclusive message range and optional run label of a listing.
	Range struct {
		From    index.MessageID `json:"from"`
		Through index.MessageID `json:"through"`
		Run     string          `json:"run,omitempty"`
	}
)

// EncodeCursor returns deterministic, unpadded base32. It signs no authority.
func EncodeCursor(cursor Cursor) (string, error) {
	if !validCursor(cursor) {
		return "", invalidInput()
	}
	data, err := json.Marshal(cursor, json.Deterministic(true))
	if err != nil {
		return "", invalidInput()
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data), nil
}

// DecodeCursor checks only encoding and shape. Even a successfully decoded
// selector and position must pass chain resolution and authorization later.
func DecodeCursor(token string) (Cursor, error) {
	data, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(token)
	if err != nil {
		return Cursor{}, invalidInput()
	}
	var cursor Cursor
	fields, err := decodeObject(data, &cursor, "kind", "position")
	if err != nil {
		return Cursor{}, err
	}
	// Find additionally requires an anchor; even empty extra selectors fail.
	fieldCount := 3
	if cursor.Kind == Find {
		fieldCount++
	}
	if len(fields) != fieldCount || !validCursor(cursor) {
		return Cursor{}, invalidInput()
	}
	if cursor.Range != nil {
		if _, err := decodeObject(fields["range"], cursor.Range, "from", "through"); err != nil {
			return Cursor{}, err
		}
	}

	if cursor.Anchor != nil {
		if _, err := decodeObject(fields["anchor"], cursor.Anchor, "run", "entry", "chunk"); err != nil {
			return Cursor{}, err
		}
	}

	return cursor, nil
}

func validCursor(cursor Cursor) bool {
	if cursor.Position < 0 {
		return false
	}
	switch cursor.Kind {
	case List:
		if cursor.Anchor != nil || cursor.Scan != "" || (cursor.Handle != "") == (cursor.Range != nil) {
			return false
		}
		return cursor.Range == nil || (cursor.Range.From > 0 && cursor.Range.Through >= cursor.Range.From)
	case Find:
		return cursor.Scan != "" && cursor.Handle == "" && cursor.Range == nil && cursor.Anchor != nil && cursor.Anchor.Run != "" && cursor.Anchor.Entry > 0 && cursor.Anchor.Chunk >= 0
	case Read:
		return false
	default:
		return false
	}
}
