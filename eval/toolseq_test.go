package eval

import (
	"math"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
)

func TestRegressionMath(t *testing.T) {
	for _, tc := range []struct {
		name           string
		bp, br, cp, cr int
		want           string
	}{
		{"improved", 3, 5, 5, 5, "improved"}, {"regressed", 2, 2, 3, 4, "regressed"},
		{"unchanged", 1, 2, 2, 4, "unchanged"}, {"not run baseline", 0, 0, 1, 1, "not_comparable"},
		{"not run candidate", 1, 1, 0, 0, "not_comparable"}, {"pending is not passed", 1, 1, 0, 1, "regressed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, regression(tc.bp, tc.br, tc.cp, tc.cr))
		})
	}
}

func TestReportMedians(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []int
		want   int
	}{
		{"empty", nil, 0}, {"odd", []int{9, 1, 3}, 3}, {"even floor", []int{4, 1, 9, 3}, 3},
		{"large", []int{math.MaxInt - 1, math.MaxInt}, math.MaxInt - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, medianInteger(tc.values))
		})
	}
	assert.Equal(t, 3*time.Nanosecond, medianInteger([]time.Duration{4, 3}))
	assert.InDelta(t, 0.75, medianScore([]float64{0.5, 1}), 0)
}

func TestToolSequenceDepthFirst(t *testing.T) {
	child := &trajectory.Trajectory{Steps: []trajectory.Step{{Tool: &trajectory.ToolStep{Name: "lookup"}}, {Tool: &trajectory.ToolStep{Name: "refund"}}}}
	session := trajectory.Session{{Steps: []trajectory.Step{{Tool: &trajectory.ToolStep{Name: "delegate", Children: []*trajectory.Trajectory{child}}}, {Tool: &trajectory.ToolStep{Name: "notify"}}}}, {Steps: []trajectory.Step{{Tool: &trajectory.ToolStep{Name: "finish"}}}}}
	assert.Equal(t, []string{"delegate", "lookup", "refund", "notify", "finish"}, toolSequence(session))
}

func TestToolSequenceEdits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		base, cand []string
		want       []ToolEdit
	}{
		{"delete", []string{"lookup", "refund"}, []string{"lookup"}, []ToolEdit{{Op: "-", Name: "refund"}}},
		{"insert", []string{"lookup"}, []string{"lookup", "refund"}, []ToolEdit{{Op: "+", Name: "refund"}}},
		{"equal", []string{"lookup"}, []string{"lookup"}, nil},
		{"repeated", []string{"a", "b", "a"}, []string{"a", "a"}, []ToolEdit{{Op: "-", Name: "b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, toolEdits(tc.base, tc.cand))
		})
	}
}

func TestModalSequenceTie(t *testing.T) {
	samples := make([]sequenceSample, 0, 3)
	samples = append(samples, sequenceSample{sequence: []string{"lookup", "refund"}, repeat: 4}, sequenceSample{sequence: []string{"lookup"}, repeat: 1})
	assert.Equal(t, ToolSequence{Sequence: []string{"lookup"}, Count: 1, Total: 2}, modalSequence(samples))
	samples = append(samples, sequenceSample{sequence: []string{"lookup", "refund"}, repeat: 5})
	assert.Equal(t, ToolSequence{Sequence: []string{"lookup", "refund"}, Count: 2, Total: 3}, modalSequence(samples))
}
