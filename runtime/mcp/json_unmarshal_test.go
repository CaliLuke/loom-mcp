package mcp

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type canonicalNamedKey string

type canonicalDecoded struct {
	DisplayName string
	Count       int8
	Unsigned    uint16
	Ratio       float32
	Enabled     bool
	Viewport    *canonicalViewport
	Labels      map[canonicalNamedKey]int
	Items       []canonicalViewport
	Pair        [2]int
	Bytes       []byte
	Ignored     string `json:"-"`
}

type canonicalCustom string

func (c *canonicalCustom) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = canonicalCustom("custom:" + value)
	return nil
}

func TestUnmarshalCanonicalJSONDecodesCompleteShape(t *testing.T) {
	data := []byte(`{
		"display_name":"home","count":7,"unsigned":9,"ratio":1.5,"enabled":true,
		"viewport":{"width":10,"height":20},"labels":{"primary":1},
		"items":[{"width":2,"height":3}],"pair":[4,5],"bytes":"AQI="
	}`)
	var decoded canonicalDecoded

	require.NoError(t, UnmarshalCanonicalJSON(data, &decoded))

	assert.Equal(t, "home", decoded.DisplayName)
	assert.Equal(t, int8(7), decoded.Count)
	assert.Equal(t, uint16(9), decoded.Unsigned)
	assert.InDelta(t, 1.5, decoded.Ratio, 0)
	assert.True(t, decoded.Enabled)
	assert.Equal(t, &canonicalViewport{Width: 10, Height: 20}, decoded.Viewport)
	assert.Equal(t, map[canonicalNamedKey]int{"primary": 1}, decoded.Labels)
	assert.Equal(t, []canonicalViewport{{Width: 2, Height: 3}}, decoded.Items)
	assert.Equal(t, [2]int{4, 5}, decoded.Pair)
	assert.Equal(t, []byte{1, 2}, decoded.Bytes)
}

func TestUnmarshalCanonicalJSONSupportsInterfacesPointersNullAndUnmarshalers(t *testing.T) {
	type payload struct {
		Value  any
		Nested **canonicalViewport
		Custom canonicalCustom
	}
	var decoded payload
	require.NoError(t, UnmarshalCanonicalJSON([]byte(`{"value":{"n":1},"nested":{"width":3},"custom":"value"}`), &decoded))
	assert.Equal(t, map[string]any{"n": jsontext.Value("1")}, decoded.Value)
	require.NotNil(t, decoded.Nested)
	require.NotNil(t, *decoded.Nested)
	assert.Equal(t, 3, (*decoded.Nested).Width)
	assert.Equal(t, canonicalCustom("custom:value"), decoded.Custom)

	require.NoError(t, UnmarshalCanonicalJSON([]byte(`{"nested":null,"custom":null}`), &decoded))
	assert.Nil(t, decoded.Nested)
	assert.Empty(t, decoded.Custom)
}

func TestUnmarshalCanonicalJSONRejectsMalformedAndUnsafeAssignments(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		target  func() any
		message string
	}{
		{name: "invalid_json", data: `{`, target: func() any { return &canonicalDecoded{} }, message: "unexpected EOF"},
		{name: "trailing_json", data: `{}` + `{}`, target: func() any { return &canonicalDecoded{} }, message: "unexpected trailing JSON data"},
		{name: "unknown_field", data: `{"other":1}`, target: func() any { return &canonicalDecoded{} }, message: `unknown field "other"`},
		{name: "wrong_struct_shape", data: `[]`, target: func() any { return &canonicalDecoded{} }, message: "cannot handle JSON array"},
		{name: "int_overflow", data: `{"count":128}`, target: func() any { return &canonicalDecoded{} }, message: `within "/count"`},
		{name: "negative_uint", data: `{"unsigned":-1}`, target: func() any { return &canonicalDecoded{} }, message: `within "/unsigned"`},
		{name: "fractional_int", data: `{"count":1.5}`, target: func() any { return &canonicalDecoded{} }, message: `within "/count"`},
		{name: "array_length", data: `{"pair":[1]}`, target: func() any { return &canonicalDecoded{} }, message: `within "/pair"`},
		{name: "slice_item", data: `{"items":[{"width":"bad"}]}`, target: func() any { return &canonicalDecoded{} }, message: `within "/items/0/width"`},
		{name: "non_string_map_key", data: `{"a":1}`, target: func() any { value := map[int]int{}; return &value }, message: "cannot handle JSON object"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := UnmarshalCanonicalJSON([]byte(tt.data), tt.target())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}

	for _, target := range []any{nil, canonicalDecoded{}, (*canonicalDecoded)(nil)} {
		err := UnmarshalCanonicalJSON([]byte(`{}`), target)
		var invalid *json.SemanticError
		require.ErrorAs(t, err, &invalid)
	}
}

func TestUnmarshalCanonicalJSONReportsMapAndIndexFields(t *testing.T) {
	var mapped map[canonicalNamedKey]int
	err := UnmarshalCanonicalJSON([]byte(`{"valid":1,"invalid":"bad"}`), &mapped)
	require.Error(t, err)
	var typeErr *json.SemanticError
	require.ErrorAs(t, err, &typeErr)
	assert.Equal(t, jsontext.Pointer("/invalid"), typeErr.JSONPointer)

	var values []int
	err = UnmarshalCanonicalJSON([]byte(`[1,"bad"]`), &values)
	require.ErrorAs(t, err, &typeErr)
	assert.Equal(t, jsontext.Pointer("/1"), typeErr.JSONPointer)
}
