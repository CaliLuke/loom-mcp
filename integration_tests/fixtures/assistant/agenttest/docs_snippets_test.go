package agenttest_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type snippetDocument struct {
	text  string
	allGo bool
}

func TestDocsSnippetsMatchCompiledFixture(t *testing.T) {
	repo, err := os.OpenRoot(filepath.Clean("../../../.."))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, repo.Close())
	})
	documents := make(map[string]snippetDocument)
	// This explicit manifest owns every guide/skill Go block and runtime's
	// agenttest, expect and trajectory blocks. Unrelated runtime blocks stay out.
	for _, path := range []string{"docs/agent_testing.md", ".agents/skills/loom-mcp/references/agent-testing.md", "docs/runtime.md"} {
		body, err := repo.ReadFile(path)
		require.NoError(t, err)
		documents[path] = snippetDocument{text: string(body), allGo: path != "docs/runtime.md"}
	}
	sources := make(map[string]string)
	for _, pattern := range []string{"integration_tests/fixtures/assistant/agenttest/*_test.go", "integration_tests/fixtures/assistant/eval/*_test.go"} {
		paths, err := fs.Glob(repo.FS(), pattern)
		require.NoError(t, err)
		for _, path := range paths {
			body, err := repo.ReadFile(path)
			require.NoError(t, err)
			sources[path] = string(body)
		}
	}
	require.NoError(t, checkDocsSnippets(documents, sources))
	for _, link := range []struct{ path, needle string }{
		{".agents/skills/loom-mcp/SKILL.md", "references/agent-testing.md"},
		{".agents/skills/loom-mcp/references/user-guides/testing.md", "agenttest"},
		{".agents/skills/loom-mcp/references/repo-map.md", "agenttest"},
		{"README.md", "docs/agent_testing.md"},
	} {
		body, err := repo.ReadFile(link.path)
		require.NoError(t, err)
		assert.Contains(t, string(body), link.needle, link.path)
	}
}

func TestDocsSnippetParser(t *testing.T) {
	const source = "// snippet:start selection\n\tcall()\n\t\tmore()  \n// snippet:end selection\n"
	const doc = "<!-- snippet: selection -->\n```go\ncall()\n    more()\n```\n"
	cases := []struct {
		name, doc, source, want string
		allGo                   bool
	}{
		{"matching indentation", doc, source, "", true},
		{"drift", strings.ReplaceAll(doc, "call()", "wrong()"), source, "drift", true},
		{"unknown name", strings.ReplaceAll(doc, "selection", "unknown"), source, "unknown snippet", true},
		{"duplicate region", doc, source + source, "defined twice", true},
		{"duplicate across files", doc, source, "defined twice", true},
		{"unused region", "", source, "unused snippet", true},
		{"marker without fence", "<!-- snippet: selection -->\ntext\n", source, "must precede a go fence", true},
		{"marker separated from fence", "<!-- snippet: selection -->\n\n```go\ncall()\n```", source, "must precede a go fence", true},
		{"unmarked guide block", "```go\ncall()\n```", source, "unmarked go block", true},
		{"unmarked runtime block", "```go\nagenttest.New()\n```", source, "unmarked go block", false},
		{"unmarked expect block", "```go\nexpect.Calls()\n```", source, "unmarked go block", false},
		{"unmarked trajectory block", "```go\ntrajectory.Build()\n```", source, "unmarked go block", false},
		{"unrelated runtime block", doc + "```go\nother()\n```", source, "", false},
		{"over budget", "<!-- snippet: selection -->\n```go\n" + strings.Repeat("call()\n", 31) + "```",
			"// snippet:start selection\n" + strings.Repeat("call()\n", 31) + "// snippet:end selection", "exceeds 30 lines", true},
		{"at budget", "<!-- snippet: selection -->\n```go\n" + strings.Repeat("call()\n", 30) + "```",
			"// snippet:start selection\n" + strings.Repeat("call()\n", 30) + "// snippet:end selection", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := map[string]string{"fixture.go": tc.source}
			if tc.name == "duplicate across files" {
				sources["second.go"] = tc.source
			}
			err := checkDocsSnippets(map[string]snippetDocument{"guide.md": {text: tc.doc, allGo: tc.allGo}}, sources)
			if tc.want == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.want)
			}
		})
	}
}

// checkDocsSnippets checks the whole manifest so orphaned source regions fail too.
func checkDocsSnippets(documents map[string]snippetDocument, sources map[string]string) error {
	regions := make(map[string]string)
	for _, path := range sortedSnippetPaths(sources) {
		if err := readSnippetRegions(sources[path], regions); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	used := make(map[string]bool)
	for _, path := range sortedSnippetPaths(documents) {
		if err := checkSnippetDocument(documents[path], regions, used); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	for _, name := range sortedSnippetPaths(regions) {
		if !used[name] {
			return fmt.Errorf("unused snippet %q", name)
		}
	}
	return nil
}

// readSnippetRegions extracts named regions from compiled fixture sources.
func readSnippetRegions(source string, regions map[string]string) error {
	lines := strings.Split(source, "\n")
	name, start := "", 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if next, ok := strings.CutPrefix(trimmed, "// snippet:start "); ok {
			if name != "" {
				return fmt.Errorf("nested snippet %q", next)
			}
			if _, duplicate := regions[next]; duplicate {
				return fmt.Errorf("snippet %q defined twice", next)
			}
			name, start = next, i+1
		} else if end, ok := strings.CutPrefix(trimmed, "// snippet:end "); ok {
			if name == "" || end != name {
				return fmt.Errorf("unexpected snippet end %q", end)
			}
			if name == "selection" && i-start > 30 {
				return fmt.Errorf("selection snippet exceeds 30 lines: %d", i-start)
			}
			regions[name] = normalizeSnippet(lines[start:i])
			name = ""
		}
	}
	if name != "" {
		return fmt.Errorf("unclosed snippet %q", name)
	}
	return nil
}

// checkSnippetDocument requires adjacent markers for every in-scope Go fence.
func checkSnippetDocument(doc snippetDocument, regions map[string]string, used map[string]bool) error {
	lines := strings.Split(doc.text, "\n")
	for i := 0; i < len(lines); i++ {
		name := ""
		if marker, ok := strings.CutPrefix(lines[i], "<!-- snippet: "); ok {
			var closed bool
			name, closed = strings.CutSuffix(marker, " -->")
			if !closed || i+1 >= len(lines) || lines[i+1] != "```go" {
				return fmt.Errorf("snippet marker must precede a go fence at line %d", i+1)
			}
			i++
		}
		if lines[i] != "```go" {
			continue
		}
		start := i + 1
		i = start
		for i < len(lines) && lines[i] != "```" {
			i++
		}
		if i == len(lines) {
			return fmt.Errorf("unclosed go fence at line %d", start)
		}
		body := strings.Join(lines[start:i], "\n")
		inScope := doc.allGo || strings.Contains(body, "agenttest.") || strings.Contains(body, "expect.") || strings.Contains(body, "trajectory.")
		if name == "" {
			if inScope {
				return fmt.Errorf("unmarked go block at line %d", start)
			}
			continue
		}
		want, ok := regions[name]
		if !ok {
			return fmt.Errorf("unknown snippet %q", name)
		}
		if normalizeSnippet(lines[start:i]) != want {
			return fmt.Errorf("snippet %q drift at line %d", name, start)
		}
		used[name] = true
	}
	return nil
}

// normalizeSnippet removes common leading tabs, expands remaining indentation,
// and strips trailing whitespace without changing interior text.
func normalizeSnippet(lines []string) string {
	common := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		tabs := len(line) - len(strings.TrimLeft(line, "\t"))
		if common == -1 || tabs < common {
			common = tabs
		}
	}
	normalized := make([]string, len(lines))
	for i, line := range lines {
		tabs := len(line) - len(strings.TrimLeft(line, "\t"))
		if common > 0 {
			line = line[min(common, tabs):]
			tabs = max(0, tabs-common)
		}
		normalized[i] = strings.TrimRight(strings.Repeat("    ", tabs)+line[tabs:], " \t\r")
	}
	return strings.Join(normalized, "\n")
}

// sortedSnippetPaths makes diagnostics independent of map iteration order.
func sortedSnippetPaths[T any](entries map[string]T) []string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
