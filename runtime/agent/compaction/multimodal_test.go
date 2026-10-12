package compaction

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalSupportFold(t *testing.T) {
	wide := model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG, model.ImageFormatJPEG}, MaxImageBytes: 100}
	narrow := model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG}, MaxImageBytes: 50}
	assert.Empty(t, ToolResultParts(State{}))
	facts := []contextmeasure.RequestFacts{{Model: "z", ViewBearing: true, ToolResultParts: wide}, {Model: "a", ViewBearing: true, ToolResultParts: narrow}}
	state, err := ObserveEffective(State{}, facts, Settings{})
	require.NoError(t, err)
	assert.Equal(t, narrow, ToolResultParts(state))
	reverse, err := ObserveEffective(State{}, []contextmeasure.RequestFacts{facts[1], facts[0]}, Settings{})
	require.NoError(t, err)
	assert.Equal(t, ToolResultParts(state), ToolResultParts(reverse))
	state = Observe(state, []contextmeasure.RequestFacts{{Model: "a", ViewBearing: true, ToolResultParts: wide}, {Model: "side", ToolResultParts: model.ToolResultPartSupport{}}})
	assert.Equal(t, narrow, ToolResultParts(state))
	state = Observe(state, []contextmeasure.RequestFacts{{Model: "unsupported", ViewBearing: true}})
	assert.Empty(t, ToolResultParts(state).ImageFormats)
}
