package mcp

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type canonicalViewport struct {
	Width  int
	Height int
}

type canonicalPage struct {
	DisplayName string
	Viewport    *canonicalViewport
	Labels      map[string]string
}

type canonicalTaggedViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type canonicalTaggedPage struct {
	DisplayName string                   `json:"display_name"`
	Viewport    *canonicalTaggedViewport `json:"viewport,omitempty"`
	Labels      map[string]string        `json:"labels,omitempty"`
}

func TestMarshalCanonicalJSONNormalizesPointerValues(t *testing.T) {
	t.Parallel()

	page := canonicalPage{DisplayName: "home"}
	pagePtr := &page

	tests := []struct {
		name  string
		input any
		want  string
	}{
		{
			name:  "value input",
			input: page,
			want:  `{"display_name":"home"}`,
		},
		{
			name:  "pointer input",
			input: &page,
			want:  `{"display_name":"home"}`,
		},
		{
			name:  "double pointer input",
			input: &pagePtr,
			want:  `{"display_name":"home"}`,
		},
		{
			name:  "nil pointer input",
			input: (*canonicalPage)(nil),
			want:  `null`,
		},
		{
			name:  "interface wrapped pointer field",
			input: struct{ Payload any }{Payload: &page},
			want:  `{"payload":{"display_name":"home"}}`,
		},
		{
			name: "pointer input with populated nested pointer",
			input: &canonicalPage{
				DisplayName: "home",
				Viewport:    &canonicalViewport{Width: 1, Height: 2},
			},
			want: `{"display_name":"home","viewport":{"width":1,"height":2}}`,
		},
		{
			name:  "pointer input omits nil optional sub-object",
			input: &canonicalPage{DisplayName: "home", Viewport: nil},
			want:  `{"display_name":"home"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := MarshalCanonicalJSON(tt.input)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(data))
		})
	}
}

func TestMarshalCanonicalJSONPointerAndValueOutputsMatch(t *testing.T) {
	t.Parallel()

	page := canonicalPage{
		DisplayName: "home",
		Viewport:    &canonicalViewport{Width: 3},
		Labels:      map[string]string{"env": "test"},
	}

	valueData, err := MarshalCanonicalJSON(page)
	require.NoError(t, err)
	pointerData, err := MarshalCanonicalJSON(&page)
	require.NoError(t, err)
	assert.Equal(t, string(valueData), string(pointerData))
}

func TestMarshalCanonicalJSONTaggedStruct(t *testing.T) {
	t.Parallel()

	data, err := MarshalCanonicalJSON(canonicalTaggedPage{
		DisplayName: "home",
		Viewport:    &canonicalTaggedViewport{Width: 1920, Height: 1080},
		Labels:      map[string]string{"environment": "test"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"display_name":"home",
		"viewport":{"width":1920,"height":1080},
		"labels":{"environment":"test"}
	}`, string(data))
}

func TestMarshalCanonicalJSONPointerMapKeyFailFast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
	}{
		{
			name:  "pointer to int-keyed map",
			input: &map[int]string{1: "one"},
		},
		{
			name:  "pointer to struct with int-keyed map field",
			input: &struct{ Counts map[int]int }{Counts: map[int]int{1: 1}},
		},
		{
			name:  "interface wrapped int-keyed map",
			input: struct{ Payload any }{Payload: map[int]string{1: "one"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := MarshalCanonicalJSON(tt.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported map key type")
		})
	}
}

func TestCanMarshalJSONDirectly(t *testing.T) {
	type untagged struct {
		DisplayName string
	}
	type embedded struct {
		canonicalTaggedViewport
	}
	type interfaceField struct {
		Value any `json:"value"`
	}
	type stringOption struct {
		Count int `json:"count,string"`
	}
	type duplicateNames struct {
		First  string `json:"name"`
		Second string `json:"name"`
	}
	type recursive struct {
		Next *recursive `json:"next,omitempty"`
	}
	type zeroStructOmitEmpty struct {
		Viewport canonicalTaggedViewport `json:"viewport,omitempty"`
	}
	emptyTagName := reflect.StructOf([]reflect.StructField{{
		Name: "DisplayName",
		Type: reflect.TypeOf(""),
		Tag:  `json:",omitempty"`,
	}})
	invalidTagName := reflect.StructOf([]reflect.StructField{{
		Name: "Value",
		Type: reflect.TypeOf(""),
		Tag:  `json:"display-name"`,
	}})
	skippedWithOptions := reflect.StructOf([]reflect.StructField{{
		Name: "Value",
		Type: reflect.TypeOf(""),
		Tag:  `json:"-,omitempty"`,
	}})

	tests := []struct {
		name   string
		typeOf reflect.Type
		want   bool
	}{
		{name: "tagged struct", typeOf: reflect.TypeOf(canonicalTaggedPage{}), want: true},
		{name: "pointer to tagged struct", typeOf: reflect.TypeOf(&canonicalTaggedPage{}), want: true},
		{name: "untagged struct", typeOf: reflect.TypeOf(untagged{}), want: false},
		{name: "embedded struct", typeOf: reflect.TypeOf(embedded{}), want: false},
		{name: "interface field", typeOf: reflect.TypeOf(interfaceField{}), want: false},
		{name: "non-string map key", typeOf: reflect.TypeOf(map[int]string{}), want: false},
		{name: "string tag option", typeOf: reflect.TypeOf(stringOption{}), want: false},
		{name: "empty tag name", typeOf: emptyTagName, want: false},
		{name: "duplicate names", typeOf: reflect.TypeOf(duplicateNames{}), want: false},
		{name: "recursive struct", typeOf: reflect.TypeOf(recursive{}), want: false},
		{name: "zero struct omitempty", typeOf: reflect.TypeOf(zeroStructOmitEmpty{}), want: false},
		{name: "non-generated tag name", typeOf: invalidTagName, want: false},
		{name: "skip tag with options", typeOf: skippedWithOptions, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, canMarshalJSONDirectly(tt.typeOf))
		})
	}
}

func BenchmarkMarshalCanonicalJSON(b *testing.B) {
	page := canonicalPage{
		DisplayName: "home",
		Viewport:    &canonicalViewport{Width: 1920, Height: 1080},
		Labels:      map[string]string{"environment": "test", "region": "local"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := MarshalCanonicalJSON(page)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalCanonicalJSON(b *testing.B) {
	data := []byte(`{"display_name":"home","viewport":{"width":1920,"height":1080},"labels":{"environment":"test","region":"local"}}`)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var page canonicalPage
		if err := UnmarshalCanonicalJSON(data, &page); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalCanonicalJSONTagged(b *testing.B) {
	page := canonicalTaggedPage{
		DisplayName: "home",
		Viewport:    &canonicalTaggedViewport{Width: 1920, Height: 1080},
		Labels:      map[string]string{"environment": "test", "region": "local"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := MarshalCanonicalJSON(page); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalStandardJSONTagged(b *testing.B) {
	page := canonicalTaggedPage{
		DisplayName: "home",
		Viewport:    &canonicalTaggedViewport{Width: 1920, Height: 1080},
		Labels:      map[string]string{"environment": "test", "region": "local"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := json.Marshal(page); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalCanonicalJSONTagged(b *testing.B) {
	data := []byte(`{"display_name":"home","viewport":{"width":1920,"height":1080},"labels":{"environment":"test","region":"local"}}`)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var page canonicalTaggedPage
		if err := UnmarshalCanonicalJSON(data, &page); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalStandardJSONTagged(b *testing.B) {
	data := []byte(`{"display_name":"home","viewport":{"width":1920,"height":1080},"labels":{"environment":"test","region":"local"}}`)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var page canonicalTaggedPage
		if err := json.Unmarshal(data, &page); err != nil {
			b.Fatal(err)
		}
	}
}
