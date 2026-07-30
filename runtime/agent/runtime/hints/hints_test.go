package hints

import (
	"strings"
	"testing"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHintRegistryFormatsCallAndResultTemplates(t *testing.T) {
	t.Parallel()

	callID := tools.Ident("hints.test.call")
	resultID := tools.Ident("hints.test.result")
	RegisterCallHint(callID, template.Must(template.New("call").Parse("Calling {{.Name}}")))
	RegisterResultHints(map[tools.Ident]*template.Template{
		resultID: template.Must(template.New("result").Parse("Found {{.Count}}")),
		"":       template.Must(template.New("ignored").Parse("ignored")),
	})

	assert.Equal(t, "Calling loom", FormatCallHint(callID, map[string]any{"Name": "loom"}))
	assert.Equal(t, "Found 2", FormatResultHint(resultID, map[string]any{"Count": 2}))
	assert.Empty(t, FormatCallHint("hints.test.missing", nil))
	assert.Empty(t, FormatResultHint("hints.test.missing", nil))

	RegisterCallHints(map[tools.Ident]*template.Template{
		"hints.test.render_error": template.Must(template.New("error").Option("missingkey=error").Parse("{{.Missing}}")),
	})
	assert.Empty(t, FormatCallHint("hints.test.render_error", map[string]any{}))
}

func TestCompileHintTemplatesSupportsOptionalFieldsTypedCountsAndUnicode(t *testing.T) {
	t.Parallel()

	compiled, err := CompileHintTemplates(map[tools.Ident]string{
		"hints.test.helpers": `{{with .Optional}}{{.}}{{else}}fallback{{end}}|{{count .Items}}|{{truncate .Text 1}}`,
	}, nil)
	require.NoError(t, err)
	RegisterCallHints(compiled)

	got := FormatCallHint("hints.test.helpers", map[string]any{
		"Items": []string{"one", "two"},
		"Text":  "éclair",
	})
	require.True(t, utf8.ValidString(got))
	assert.Equal(t, "fallback|2|é", got)
}

func TestCompileHintTemplatesValidation(t *testing.T) {
	t.Parallel()

	compiled, err := CompileHintTemplates(nil, nil)
	require.NoError(t, err)
	assert.Nil(t, compiled)
	compiled, err = CompileHintTemplates(map[tools.Ident]string{"empty": ""}, nil)
	require.NoError(t, err)
	assert.Empty(t, compiled)
	_, err = CompileHintTemplates(map[tools.Ident]string{"broken": "{{"}, nil)
	require.ErrorContains(t, err, "compile hint for broken")
}

func TestCompileHintTemplates_Since(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		from    any
		to      any
		want    string
		wantErr bool
	}

	cases := []testCase{
		{
			name: "rfc3339_z",
			from: "2025-01-01T00:00:00Z",
			to:   "2025-01-01T00:00:10Z",
			want: "10",
		},
		{
			name: "rfc3339_offset_negative",
			from: "2025-01-01T00:00:00-08:00",
			to:   "2025-01-01T00:00:00Z",
			want: "-28800",
		},
		{
			name: "invalid_returns_zero",
			from: "not-a-time",
			to:   "2025-01-01T00:00:00Z",
			want: "0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := map[tools.Ident]string{
				tools.Ident("t"): "{{ since .From .To }}",
			}
			compiled, err := CompileHintTemplates(raw, template.FuncMap{})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("CompileHintTemplates error: %v", err)
			}

			tmpl := compiled[tools.Ident("t")]
			if tmpl == nil {
				t.Fatalf("expected compiled template")
			}

			var b strings.Builder
			if err := tmpl.Execute(&b, map[string]any{
				"From": tc.from,
				"To":   tc.to,
			}); err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if got := strings.TrimSpace(b.String()); got != tc.want {
				t.Fatalf("unexpected output: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestCompileHintTemplates_HumanTime(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		in   any
		want string
	}

	cases := []testCase{
		{
			name: "rfc3339_z",
			in:   "2025-01-01T00:00:00Z",
			want: "Jan 1, 12:00 AM",
		},
		{
			name: "rfc3339_offset",
			in:   "2025-01-01T00:00:00-08:00",
			want: "Jan 1, 12:00 AM",
		},
		{
			name: "invalid_falls_back_to_input",
			in:   "not-a-time",
			want: "not-a-time",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw := map[tools.Ident]string{
				tools.Ident("t"): "{{ humanTime .At }}",
			}
			compiled, err := CompileHintTemplates(raw, template.FuncMap{})
			if err != nil {
				t.Fatalf("CompileHintTemplates error: %v", err)
			}

			tmpl := compiled[tools.Ident("t")]
			if tmpl == nil {
				t.Fatalf("expected compiled template")
			}

			var b strings.Builder
			if err := tmpl.Execute(&b, map[string]any{
				"At": tc.in,
			}); err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if got := strings.TrimSpace(b.String()); got != tc.want {
				t.Fatalf("unexpected output: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestCompileHintTemplates_TimestampAliases(t *testing.T) {
	t.Parallel()

	type aliasString string
	type aliasTime time.Time
	at := aliasString("2025-01-01T00:00:00Z")
	toTime := aliasTime(time.Date(2025, 1, 1, 0, 0, 10, 0, time.UTC))

	raw := map[tools.Ident]string{
		tools.Ident("t"): "{{ since .From .To }}",
	}
	compiled, err := CompileHintTemplates(raw, template.FuncMap{})
	if err != nil {
		t.Fatalf("CompileHintTemplates error: %v", err)
	}

	var b strings.Builder
	if err := compiled[tools.Ident("t")].Execute(&b, map[string]any{
		"From": &at,
		"To":   &toTime,
	}); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := strings.TrimSpace(b.String()); got != "10" {
		t.Fatalf("unexpected output: got %q want %q", got, "10")
	}
}
