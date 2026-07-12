package toolerrors

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstructorsProduceUsefulMessages(t *testing.T) {
	require.EqualError(t, New(""), "tool error")
	require.EqualError(t, NewWithCause("", nil), "tool error")
	require.EqualError(t, NewWithCause("", errors.New("denied")), "denied")
	assert.EqualError(t, Errorf("tool %s failed", "search"), "tool search failed")
}

func TestFromErrorPreservesEveryWrappingLayer(t *testing.T) {
	inner := New("denied")
	converted := FromError(fmt.Errorf("execute search: %w", inner))

	require.EqualError(t, converted, "execute search: denied")
	require.Error(t, converted.Cause)
	require.EqualError(t, converted.Cause, "denied")
	assert.Same(t, inner, converted.Cause)
	assert.ErrorIs(t, converted, inner)
}

func TestFromErrorReusesDirectToolErrorAndHandlesNil(t *testing.T) {
	toolErr := New("failed")
	assert.Same(t, toolErr, FromError(toolErr))
	assert.Nil(t, FromError(nil))
	assert.Empty(t, (*ToolError)(nil).Error())
	assert.NoError(t, (*ToolError)(nil).Unwrap())
}

func TestToolErrorChainSurvivesJSONRoundTrip(t *testing.T) {
	original := NewWithCause("outer", NewWithCause("middle", errors.New("inner")))

	payload, err := json.Marshal(original)
	require.NoError(t, err)
	var decoded ToolError
	require.NoError(t, json.Unmarshal(payload, &decoded))

	require.EqualError(t, &decoded, "outer")
	require.Error(t, decoded.Cause)
	require.EqualError(t, decoded.Cause, "middle")
	require.Error(t, decoded.Cause.Cause)
	require.EqualError(t, decoded.Cause.Cause, "inner")
	var asToolError *ToolError
	assert.ErrorAs(t, &decoded, &asToolError)
}
