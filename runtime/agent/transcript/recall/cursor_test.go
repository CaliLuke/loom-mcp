package recall

import (
	"encoding/base32"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	for _, cursor := range []Cursor{
		{Kind: List, Handle: "rc-12345678", Position: 0},
		{Kind: List, Range: &Range{From: 1, Through: 9, Run: "r2"}, Position: 9},
		{Kind: Find, Scan: "世界 Case", Anchor: &FindAnchor{Run: "run", Entry: 7, Chunk: 1}, Position: 8000000},
		// A decoded selector is untrusted; later resolution must reject this handle.
		{Kind: List, Handle: "outside-chain", Position: 1},
	} {
		encoded, err := EncodeCursor(cursor)
		require.NoError(t, err)
		assert.Regexp(t, "^[A-Z2-7]+$", encoded)
		got, err := DecodeCursor(encoded)
		require.NoError(t, err)
		assert.Equal(t, cursor, got)
		again, err := EncodeCursor(cursor)
		require.NoError(t, err)
		assert.Equal(t, encoded, again)
	}
}

func TestCursorMalformed(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"kind":"list","handle":"x","scan":"","position":0}`, `{"kind":"find","scan":"x","handle":"","position":0}`, `{"kind":"read","handle":"x","position":0}`, `{"kind":"list","handle":"x","scan":"y","position":0}`, `{"kind":"find","handle":"x","position":0}`, `{"kind":"find","scan":"","position":0}`, `{"kind":"list","handle":"x","position":-1}`, `{"kind":"list","handle":"x"}`, `{"kind":"list","handle":"x","position":null}`, `{"kind":"list","handle":"x","position":0,"scope":"other"}`, `{"kind":"list","range":{"from":9,"through":1},"position":0}`} {
		token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(raw))
		got, err := DecodeCursor(token)
		assert.Zero(t, got)
		var typed *Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, CodeInvalidInput, typed.Code)
	}
	for _, token := range []string{"", "!bad", "M====", "MY"} {
		_, err := DecodeCursor(token)
		require.Error(t, err)
	}
	for _, cursor := range []Cursor{{}, {Kind: List, Handle: "x", Position: -1}, {Kind: Find, Scan: "x", Handle: "y"}} {
		_, err := EncodeCursor(cursor)
		require.Error(t, err)
	}
}

func TestFindCursorMalformedAnchor(t *testing.T) {
	for _, anchor := range []string{
		`null`, `{}`, `{"run":"run","entry":1}`, `{"run":"run","entry":1,"chunk":null}`,
		`{"run":"run","entry":0,"chunk":0}`, `{"run":"","entry":1,"chunk":0}`,
		`{"run":"run","entry":1,"chunk":-1}`, `{"run":"run","entry":1,"chunk":0,"scope":"other"}`,
	} {
		t.Run(anchor, func(t *testing.T) {
			raw := `{"kind":"find","scan":"needle","position":0,"anchor":` + anchor + `}`
			token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(raw))
			_, err := DecodeCursor(token)
			var typed *Error
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, CodeInvalidInput, typed.Code)
		})
	}
	for _, raw := range []string{
		`{"kind":"find","scan":"needle","position":10}`,
		`{"kind":"list","handle":"x","position":0,"anchor":{"run":"run","entry":1,"chunk":0}}`,
	} {
		token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(raw))
		_, err := DecodeCursor(token)
		var typed *Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, CodeInvalidInput, typed.Code)
	}
}
