package hints_test

import (
	"testing"
	"text/template"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type hintTimestamp string

func TestHintFormattingInvalidValuesAndOverrides(t *testing.T) {
	id := tools.Ident("boundary.format")
	compiled, err := hints.CompileHintTemplates(map[tools.Ident]string{id: `{{humanTime .At}}|{{since .At .Until}}|{{count .Items}}|{{truncate "éclair" .Limit}}|{{custom}}`}, template.FuncMap{"custom": func() string {
		return "extra"
	}})
	require.NoError(t, err)
	hints.RegisterCallHints(compiled)
	var missing *[]string
	stamp := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)
	for _, tc := range []struct {
		at, until, items any
		limit            int
		want             string
	}{
		{nil, stamp, nil, 0, "<nil>|0|0||extra"},
		{"", stamp, missing, -1, "|0|0||extra"},
		{"invalid", stamp, 7, 20, "invalid|0|0|éclair|extra"},
		{&stamp, "bad", []string{"one"}, 1, "Jan 2, 3:04 PM|0|1|é|extra"},
		{hintTimestamp("2026-01-02T15:04:00Z"), stamp, []int{1, 2}, 2, "Jan 2, 3:04 PM|0|2|éc|extra"},
	} {
		got := hints.FormatCallHint(id, map[string]any{"At": tc.at, "Until": tc.until, "Items": tc.items, "Limit": tc.limit})
		assert.Equal(t, tc.want, got)
	}
	hints.RegisterCallHint(id, nil)
	hints.RegisterCallHints(nil)
	hints.RegisterResultHints(nil)
	hints.RegisterResultHint("", compiled[id])
	hints.RegisterResultHint(id, nil)
	result, err := template.New("result").Option("missingkey=error").Parse("{{.Missing}}")
	require.NoError(t, err)
	hints.RegisterResultHint(id, result)
	assert.Empty(t, hints.FormatResultHint(id, map[string]any{}))
}

func (s hintTimestamp) String() string {
	return string(s)
}
