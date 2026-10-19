package statefulevaluation

import (
	"encoding/json/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type routeResult struct {
	Tool            string
	ToolHit         bool
	InputFieldsHit  bool
	OutputFieldsHit bool
}
type qualityResult struct {
	Case                     string
	Arm                      string
	Discovery                discoveryResult
	Routes                   []routeResult
	RequiredRouteRecall      bool
	SelectedTool             string
	Invocation               *invocation
	GeneratedExecutorSuccess bool
	WholeTaskSupported       bool
	ScriptedTaskSuccess      bool
	InitialFixedBytes        int
	InvocationFixedBytes     int
	RetainedBytes            int
	UnsupportedOutputFields  []string
}

func TestFrozenCorpusUsesActualGeneratedSchemas(t *testing.T) {
	rows := runQuality(t)
	require.Len(t, rows, 24)
	for _, row := range rows {
		if len(row.UnsupportedOutputFields) > 0 {
			assert.False(t, row.WholeTaskSupported)
			assert.False(t, row.ScriptedTaskSuccess)
		}
		if row.GeneratedExecutorSuccess {
			require.NotNil(t, row.Invocation)
			assert.Equal(t, "generated_executor", row.Invocation.Stage)
			assert.Empty(t, row.Invocation.Failure)
		}
	}
}

// runQuality uses the unchanged OR routes and field pointers from the shared corpus.
func runQuality(t *testing.T) []qualityResult {
	t.Helper()
	corpus := newEvaluationFixture(t).cases
	rows := make([]qualityResult, 0, 2*len(corpus))
	for _, c := range corpus {
		for _, arm := range []string{"compact", "native"} {
			f := newEvaluationFixture(t)
			key := discoveryKey{Agent: "a", Session: "quality"}
			_, initial := exposure(t, f, arm, key)
			found := f.discover(t, key, c.Query)
			if arm == "compact" {
				f.state = newDiscoveryState()
			}
			row := qualityResult{Case: c.ID, Arm: arm, Discovery: found, InitialFixedBytes: initial, WholeTaskSupported: len(c.Routes) > 0 && len(c.UnsupportedOutputFields) == 0, UnsupportedOutputFields: c.UnsupportedOutputFields}
			for _, route := range c.Routes {
				hit := routeResult{Tool: route.Tool}
				for _, d := range found.Descriptors {
					if d.Name != route.Tool {
						continue
					}
					hit.ToolHit = true
					hit.InputFieldsHit = containsFields(t, d.InputSchema, route.InputFields)
					hit.OutputFieldsHit = containsFields(t, d.OutputSchema, route.OutputFields)
					for _, field := range c.UnsupportedOutputFields {
						assert.False(t, containsFields(t, d.OutputSchema, []string{field}), "unsupported field became supported")
					}
				}
				row.Routes = append(row.Routes, hit)
				row.RequiredRouteRecall = row.RequiredRouteRecall || (hit.ToolHit && hit.InputFieldsHit && hit.OutputFieldsHit)
			}
			// First returned acceptable route is fixed before observing execution results.
			for _, name := range found.Names {
				for _, hit := range row.Routes {
					if hit.Tool == name && hit.ToolHit && hit.InputFieldsHit && hit.OutputFieldsHit && row.SelectedTool == "" {
						row.SelectedTool = name
					}
				}
			}
			_, row.InvocationFixedBytes = exposure(t, f, arm, key)
			if row.SelectedTool != "" {
				result := f.invoke(t, arm, key, row.SelectedTool, taskArguments(t, row.SelectedTool))
				row.Invocation = &result
				row.GeneratedExecutorSuccess = result.Stage == "generated_executor" && result.Failure == ""
				row.ScriptedTaskSuccess = row.GeneratedExecutorSuccess && row.WholeTaskSupported
			}
			row.RetainedBytes = f.state.bytes(key, f.generation)
			rows = append(rows, row)
		}
	}
	return rows
}

func taskArguments(t *testing.T, name string) map[string]any {
	t.Helper()
	switch name {
	case "summarize_text", "analyze_sentiment", "extract_keywords":
		return map[string]any{"text": "Alpha beta"}
	case "search", "search_records":
		return map[string]any{"query": "loom", "limit": 2}
	case "projected_lookup_tool", "projected_bounded_lookup_tool":
		return map[string]any{"query": "loom"}
	case "generate_dpi_spec":
		return map[string]any{"screen_title": "Checkout", "platform": "web", "density": "compact", "primary_cta": "Continue", "sections": []string{"Header", "Body"}}
	default:
		t.Fatalf("no frozen scripted arguments for %q", name)
		return nil
	}
}

// containsFields follows schema JSON pointers without numeric coercion or schema rewriting.
func containsFields(t *testing.T, data []byte, pointers []string) bool {
	t.Helper()
	var schema any
	require.NoError(t, json.Unmarshal(data, &schema))
	for _, pointer := range pointers {
		value := schema
		for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			switch node := value.(type) {
			case map[string]any:
				var ok bool
				value, ok = node[token]
				if !ok {
					return false
				}
			case []any:
				pos, err := strconv.Atoi(token)
				if err != nil || pos < 0 || pos >= len(node) {
					return false
				}
				value = node[pos]
			default:
				return false
			}
		}
	}
	return true
}
