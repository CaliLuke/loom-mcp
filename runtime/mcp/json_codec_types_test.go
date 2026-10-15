package mcp

import (
	"context"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type canonicalTextValue string

func TestCanonicalJSONRetainsBinaryAndTextMarshaledFields(t *testing.T) {
	value := struct {
		Bytes   []byte
		Text    canonicalTextValue
		Tagged  string `json:"tagged"`
		Ignored string `json:"-"`
		private string
	}{Bytes: []byte{1, 2, 3}, Text: "plain", Tagged: "value", Ignored: "secret", private: "secret"}
	data, err := MarshalCanonicalJSON(value)
	require.NoError(t, err)
	assert.JSONEq(t, `{"bytes":"AQID","text":"plain","tagged":"value"}`, string(data))
	data, err = MarshalCanonicalJSON(nil)
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

func TestCanonicalJSONOmitsNilUnsupportedOptionalFields(t *testing.T) {
	value := struct {
		Complex  complex128     `json:"complex,omitempty"`
		Channel  chan int       `json:"channel,omitempty"`
		Function func()         `json:"function,omitempty"`
		Pointer  unsafe.Pointer `json:"pointer,omitempty"`
		Keep     string         `json:"keep"`
	}{Keep: "value"}
	data, err := MarshalCanonicalJSON(value)
	require.NoError(t, err)
	assert.JSONEq(t, `{"keep":"value"}`, string(data))
}

func TestCanonicalJSONRejectsUnsupportedTypedValues(t *testing.T) {
	for _, value := range []any{struct{ Value any }{complex(1, 2)}, struct{ Value any }{make(chan int)}, struct{ Value any }{func() {
	}}} {
		_, err := MarshalCanonicalJSON(value)
		require.Error(t, err)
		text, err := EncodeJSONToString(context.Background(), nil, value)
		require.Error(t, err)
		assert.Empty(t, text)
	}
	var unsupported complex128
	require.Error(t, UnmarshalCanonicalJSON([]byte(`1`), &unsupported))
}

func TestCanonicalJSONPropagatesTextMarshalingFailure(t *testing.T) {
	_, err := MarshalCanonicalJSON(struct{ Text canonicalTextValue }{})
	require.ErrorIs(t, err, assert.AnError)
}

func (v canonicalTextValue) MarshalText() ([]byte, error) {
	if v == "" {
		return nil, assert.AnError
	}
	return []byte(v), nil
}
