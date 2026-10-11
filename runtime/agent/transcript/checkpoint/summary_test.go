package checkpoint

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// TestBoundSummary checks whole, heading-free, heading-only and surplus cases.
func TestBoundSummary(t *testing.T) {
	closing := "[shortened]"
	tests := []struct {
		name, text string
		budget     int
		want       string
	}{
		{"whole", "# Goal\nshort\n", 100, "# Goal\nshort\n"},
		{"no headings", "line one\nline two\nline three\n", 24, "line one\n[shortened]"},
		{"below headings", "# A very long heading\nbody\n", len(closing), closing},
		{"surplus", "# A\nx\n# B\n1234\n5678\n9012\nextra extra extra\n", 36, "# A\nx\n# B\n1234\n5678\n9012\n[shortened]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BoundSummary(tt.text, tt.budget, closing)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, len(got), tt.budget)
		})
	}
}

// TestBoundSummaryProperty checks seeded bounds, heading order and source line prefixes.
func TestBoundSummaryProperty(t *testing.T) {
	rng := &testGenerator{state: 1103}
	for n := 0; n < 500; n++ {
		var source strings.Builder
		count := rng.Intn(10) + 1
		headings := make([]string, count)
		bodies := make([]string, count)
		for i := range headings {
			headings[i] = "# " + strings.Repeat("H", i+1) + "\n"
			bodies[i] = strings.Repeat("世界 line\n", rng.Intn(30)+1)
			source.WriteString(headings[i] + bodies[i])
		}
		budget := rng.Intn(source.Len() + 100)
		got := BoundSummary(source.String(), budget, "[shortened]")
		assert.Equal(t, got, BoundSummary(source.String(), budget, "[shortened]"))
		assert.LessOrEqual(t, len(got), budget)
		assert.True(t, utf8.ValidString(got))
		if len(got) < len(source.String()) && strings.HasSuffix(got, "[shortened]") {
			shown := strings.TrimSuffix(got, "[shortened]")
			offset := 0
			for i, heading := range headings {
				pos := strings.Index(shown, heading)
				if pos < 0 {
					continue
				}
				assert.GreaterOrEqual(t, pos, offset)
				start := pos + len(heading)
				end := len(shown)
				if i+1 < len(headings) {
					if next := strings.Index(shown[start:], headings[i+1]); next >= 0 {
						end = start + next
					}
				}
				body := shown[start:end]
				assert.True(t, strings.HasPrefix(bodies[i], body))
				assert.True(t, body == "" || strings.HasSuffix(body, "\n"))
				offset = end
			}
		}
	}
}
