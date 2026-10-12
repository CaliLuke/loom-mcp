package compaction

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSummaryRate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		models map[string]ModelState
		want   contextmeasure.Rate
	}{
		{"default", nil, contextmeasure.Rate{Tokens: 1, Bytes: 3}},
		{"unknown", map[string]ModelState{"a": {Rate: contextmeasure.Rate{Tokens: 9, Bytes: 1}}}, contextmeasure.Rate{Tokens: 1, Bytes: 3}},
		{"largest", map[string]ModelState{"z": {Known: true, Rate: contextmeasure.Rate{Tokens: 1, Bytes: 2}}, "a": {Known: true, Rate: contextmeasure.Rate{Tokens: 1, Bytes: 4}}}, contextmeasure.Rate{Tokens: 1, Bytes: 2}},
		{"ties alphabetical", map[string]ModelState{"z": {Known: true, Rate: contextmeasure.Rate{Tokens: 2, Bytes: 6}}, "a": {Known: true, Rate: contextmeasure.Rate{Tokens: 1, Bytes: 3}}}, contextmeasure.Rate{Tokens: 1, Bytes: 3}},
		{"zero bytes", map[string]ModelState{"a": {Known: true}}, contextmeasure.Rate{Tokens: 1, Bytes: 3}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SummaryRate(State{Models: tt.models}))
		})
	}
}
