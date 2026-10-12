package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type partsInfoForWrapper struct{}

func (partsInfoForWrapper) ToolResultParts(context.Context, *Request) ToolResultPartSupport {
	return ToolResultPartSupport{MaxImageBytes: 5}
}
func TestToolResultPartsWrapper(t *testing.T) {
	wrapped, err := NewClient(struct {
		Provider
		ToolResultPartsInfo
	}{nil, partsInfoForWrapper{}})
	require.NoError(t, err)
	info, ok := wrapped.(ToolResultPartsInfo)
	require.True(t, ok)
	assert.Equal(t, 5, info.ToolResultParts(t.Context(), nil).MaxImageBytes)
}
