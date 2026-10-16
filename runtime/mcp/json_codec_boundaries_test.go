package mcp

import (
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingCanonicalMarshaler struct{ malformed bool }

type rejectingCanonicalUnmarshaler struct{}

var errCanonicalTest = errors.New("custom codec refused value")

func TestCanonicalJSONRejectsMalformedDocuments(t *testing.T) {
	for _, input := range []string{"", "true false", "[1,]", "{\"a\":}", "[", "{", "tru", "\"unterminated", "1e", "null junk"} {
		t.Run(input, func(t *testing.T) {
			var result any
			require.Error(t, UnmarshalCanonicalJSON([]byte(input), &result))
		})
	}
}

func TestCanonicalJSONReportsNestedTypeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, input, path string
		dst               any
	}{
		{"string", `{"display_name":false}`, "/display_name", &canonicalPage{}},
		{"struct", `[]`, "", &canonicalPage{}},
		{"slice", `{}`, "", &[]int{}},
		{"slice item", `[true]`, "/0", &[]int{}},
		{"array size", `[1]`, "", &[2]int{}},
		{"array item", `["x",1]`, "/0", &[2]int{}},
		{"map type", `[]`, "", &map[string]int{}},
		{"map key", `{"1":2}`, "", &map[int]int{}},
		{"map item escaped key", `{"a/b~c":true}`, "/a~1b~0c", &map[string]int{}},
		{"bool", `"true"`, "", new(bool)},
		{"unsigned negative", `-1`, "", new(uint64)},
		{"unsigned overflow", `256`, "", new(uint8)},
		{"signed overflow", `128`, "", new(int8)},
		{"float overflow", `1e100`, "", new(float32)},
		{"float type", `false`, "", new(float64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := UnmarshalCanonicalJSON([]byte(tc.input), tc.dst)
			var semantic *json.SemanticError
			require.ErrorAs(t, err, &semantic)
			assert.Equal(t, tc.path, string(semantic.JSONPointer))
		})
	}
}

func TestCanonicalJSONCustomCodecsPropagateErrors(t *testing.T) {
	for _, input := range []any{
		failingCanonicalMarshaler{},
		struct{ Value any }{failingCanonicalMarshaler{}},
		struct{ Value any }{&failingCanonicalMarshaler{}},
		[]any{failingCanonicalMarshaler{}},
		map[string]any{"value": failingCanonicalMarshaler{}},
	} {
		_, err := MarshalCanonicalJSON(input)
		require.ErrorIs(t, err, errCanonicalTest)
	}
	_, err := MarshalCanonicalJSON(struct{ Value any }{failingCanonicalMarshaler{malformed: true}})
	require.Error(t, err)
	var target struct{ Value rejectingCanonicalUnmarshaler }
	require.ErrorIs(t, UnmarshalCanonicalJSON([]byte(`{"value":{}}`), &target), errCanonicalTest)
	var slice []rejectingCanonicalUnmarshaler
	require.ErrorIs(t, UnmarshalCanonicalJSON([]byte(`[{}]`), &slice), errCanonicalTest)
}

func TestCanonicalJSONOmitsZeroFieldsWithoutDroppingValues(t *testing.T) {
	type values struct {
		Untagged string
		Bool     bool              `json:"bool,omitempty"`
		Int      int               `json:"int,omitempty"`
		Uint     uint              `json:"uint,omitempty"`
		Float    float64           `json:"float,omitempty"`
		String   string            `json:"string,omitempty"`
		Slice    []int             `json:"slice,omitempty"`
		Map      map[string]int    `json:"map,omitempty"`
		Ptr      *int              `json:"ptr,omitempty"`
		Object   canonicalViewport `json:"object,omitempty"`
		Ignored  string            `json:"-"`
		private  string
	}
	data, err := MarshalCanonicalJSON(values{Untagged: "keep", Ignored: "secret", private: "secret"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"untagged":"keep"}`, string(data))
	n := 3
	data, err = MarshalCanonicalJSON(values{Untagged: "keep", Bool: true, Int: -1, Uint: 1, Float: 1.5, String: "yes", Slice: []int{1}, Map: map[string]int{"key": 2}, Ptr: &n, Object: canonicalViewport{Width: 2}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"untagged":"keep","bool":true,"int":-1,"uint":1,"float":1.5,"string":"yes","slice":[1],"map":{"key":2},"ptr":3,"object":{"width":2,"height":0}}`, string(data))
}

func (v failingCanonicalMarshaler) MarshalJSON() ([]byte, error) {
	if v.malformed {
		return []byte("{"), nil
	}
	return nil, errCanonicalTest
}

func (*rejectingCanonicalUnmarshaler) UnmarshalJSON([]byte) error {
	return errCanonicalTest
}
