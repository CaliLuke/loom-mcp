package mcp

import (
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
