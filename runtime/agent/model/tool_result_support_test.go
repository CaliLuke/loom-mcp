package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultSupport(t *testing.T) {
	support := ToolResultPartSupport{ImageFormats: []ImageFormat{"png", "jpeg"}, DocumentFormats: []DocumentFormat{"pdf"}, DocumentURISchemes: []string{"s3"}, MaxImageBytes: 2, MaxDocumentBytes: 4}
	for _, tc := range []struct {
		name   string
		part   ToolResultContentPart
		ok     bool
		reason string
	}{
		{"image", ImagePart{Format: "png", Bytes: []byte{1, 2}}, true, ""},
		{"pointer", &ImagePart{Format: "png", Bytes: []byte{1}}, true, ""},
		{"format", ImagePart{Format: "bmp", Bytes: []byte{1}}, false, "format"},
		{"large", ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}, false, "too_large"},
		{"document", &DocumentPart{Format: "pdf", Bytes: []byte{1}}, true, ""},
		{"s3", DocumentPart{Format: "pdf", URI: "s3://bucket/key"}, true, ""},
		{"uri", DocumentPart{Format: "pdf", URI: "https://example.com/x"}, false, "uri"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := support.Accepts(tc.part)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.reason, reason)
		})
	}
	ok, reason := (ToolResultPartSupport{}).Accepts(ImagePart{Format: "png", Bytes: []byte{1}})
	assert.False(t, ok)
	assert.Equal(t, "provider", reason)
	other := ToolResultPartSupport{ImageFormats: []ImageFormat{"jpeg", "png"}, MaxImageBytes: 1}
	got := support.Intersect(other)
	assert.Equal(t, ToolResultPartSupport{ImageFormats: []ImageFormat{"jpeg", "png"}, MaxImageBytes: 1}, got)
	assert.Equal(t, got, other.Intersect(support))
	assert.Equal(t, ToolResultPartSupport{}, support.Intersect(ToolResultPartSupport{}))
}

type partsInfo struct{}

func (*partsInfo) ToolResultParts(context.Context, *Request) ToolResultPartSupport {
	return ToolResultPartSupport{MaxImageBytes: 5}
}
func TestToolResultCapabilitySet(t *testing.T) {
	var missing *partsInfo
	for _, info := range []ToolResultPartsInfo{nil, missing, &partsInfo{}} {
		wrapped := WithCapabilitySet(nil, Capabilities{ToolResultParts: info})
		got, ok := wrapped.(ToolResultPartsInfo)
		if info == nil || info == missing {
			assert.False(t, ok)
			continue
		}
		require.True(t, ok)
		assert.Equal(t, 5, got.ToolResultParts(t.Context(), nil).MaxImageBytes)
	}
}

type supportContextInfo struct{}

func (supportContextInfo) ContextLimits(context.Context, *Request) (ContextLimits, string, bool) {
	return ContextLimits{}, "", false
}
func TestToolResultCapabilityCombinations(t *testing.T) {
	for bits := range 8 {
		caps := Capabilities{}
		if bits&1 != 0 {
			caps.Counter = TokenEstimator{}
		}
		if bits&2 != 0 {
			caps.Info = supportContextInfo{}
		}
		if bits&4 != 0 {
			caps.ToolResultParts = &partsInfo{}
		}
		wrapped := WithCapabilitySet(nil, caps)
		_, counter := wrapped.(TokenCounter)
		_, info := wrapped.(ContextInfo)
		_, parts := wrapped.(ToolResultPartsInfo)
		assert.Equal(t, bits&1 != 0, counter)
		assert.Equal(t, bits&2 != 0, info)
		assert.Equal(t, bits&4 != 0, parts)
	}
}
