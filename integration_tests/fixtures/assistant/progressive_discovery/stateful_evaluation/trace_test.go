package statefulevaluation

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trace struct {
	ID    string      `json:"id"`
	Steps []traceStep `json:"steps"`
}
type traceStep struct {
	Op      string         `json:"op"`
	Query   string         `json:"query"`
	Name    string         `json:"name"`
	Args    map[string]any `json:"args"`
	Routes  []string       `json:"routes"`
	Value   any            `json:"value"`
	Agent   string         `json:"agent"`
	Session string         `json:"session"`
}
type turnResult struct {
	Trace                string
	Arm                  string
	Step                 int
	Operation            string
	Agent                string
	Session              string
	Discovery            *discoveryResult
	Invocation           *invocation
	Skipped              string
	ExposedNames         []string
	FixedBytes           int
	CumulativeFixedBytes int
	RetainedNames        []string
	RetainedBytes        int
	Evictions            int
	DiscoveryRoundTrips  int
	InvocationRoundTrips int
}

const configHash = "d62b5496bcd63565417c25826068609a70a260bd46ca3dcf331bc9dac824f44e"
const tracesHash = "9aaf9e5f01d75f6c7894b4bd8de31f11e4c92d01f558f898cad430376bc33518"

func TestFrozenDiscoveryTraces(t *testing.T) {
	results := runTraces(t)
	require.NotEmpty(t, results)
	for _, row := range results {
		assert.LessOrEqual(t, len(row.RetainedNames), maxDiscoveredTools)
		assert.LessOrEqual(t, row.RetainedBytes, maxDiscoveredBytes)
		assert.Contains(t, row.ExposedNames, "search")
		if row.Arm == "native" {
			assert.NotContains(t, row.ExposedNames, "call_tool")
		}
	}
}

// loadTraces checks frozen inputs before any quality or timing measurement.
func loadTraces(t *testing.T) []trace {
	t.Helper()
	root, err := os.OpenRoot("testdata")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, root.Close())
	}()
	config, err := root.ReadFile("config.json")
	require.NoError(t, err)
	require.Equal(t, configHash, fmt.Sprintf("%x", sha256.Sum256(config)))
	data, err := root.ReadFile("traces.json")
	require.NoError(t, err)
	require.Equal(t, tracesHash, fmt.Sprintf("%x", sha256.Sum256(data)))
	var input struct {
		Traces []trace `json:"traces"`
	}
	require.NoError(t, json.Unmarshal(data, &input))
	require.Len(t, input.Traces, 11)
	return input.Traces
}

// runTraces keeps observed ranker ordering; neither arm repairs failed retrieval.
func runTraces(t *testing.T) []turnResult {
	t.Helper()
	var results []turnResult
	for _, tr := range loadTraces(t) {
		for _, arm := range []string{"compact", "native"} {
			f := newEvaluationFixture(t)
			var found discoveryResult
			cumulative := 0
			for i, step := range tr.Steps {
				key := discoveryKey{Agent: "a", Session: "one"}
				if step.Agent != "" {
					key.Agent = step.Agent
				}
				if step.Session != "" {
					key.Session = step.Session
				}
				row := turnResult{Trace: tr.ID, Arm: arm, Step: i + 1, Operation: step.Op, Agent: key.Agent, Session: key.Session}
				// Schema exposure is the request at this turn, before its discovery result.
				row.ExposedNames, row.FixedBytes = exposure(t, f, arm, key)
				switch step.Op {
				case "discover":
					discovered := f.discover(t, key, step.Query)
					found = discovered
					row.Discovery = &discovered
					if arm == "compact" {
						f.state = newDiscoveryState()
					}
					row.DiscoveryRoundTrips = 1
				case "call", "selected_call", "route_call":
					name := selectedName(step, found.Names)
					if name == "" {
						row.Skipped = "required tool not retrieved"
					} else {
						result := f.invoke(t, arm, key, name, step.Args)
						row.Invocation = &result
						row.InvocationRoundTrips = 1
					}
				case "deny":
					value, ok := step.Value.(bool)
					require.True(t, ok)
					f.denied[key] = value
				case "generation":
					value, ok := step.Value.(string)
					require.True(t, ok)
					f.generation = value
				case "inject_stale":
					require.NoError(t, f.state.reveal(key, f.generation, descriptor{Name: "summarize_text", Description: "Explicit old-schema test snapshot", InputSchema: []byte(`{"type":"object","properties":{"old_text":{"type":"string"}},"required":["old_text"]}`), OutputSchema: []byte(`{"type":"object"}`)}))
				default:
					t.Fatalf("unknown frozen trace operation %q", step.Op)
				}
				if row.DiscoveryRoundTrips+row.InvocationRoundTrips > 0 {
					cumulative += row.FixedBytes
				}
				row.CumulativeFixedBytes = cumulative
				row.RetainedNames = f.state.names(key, f.generation)
				row.RetainedBytes = f.state.bytes(key, f.generation)
				row.Evictions = f.state.current(key, f.generation).Evictions
				results = append(results, row)
			}
		}
	}
	return results
}

func selectedName(step traceStep, names []string) string {
	if step.Op == "call" {
		return step.Name
	}
	if step.Op == "selected_call" && slices.Contains(names, step.Name) {
		return step.Name
	}
	for _, name := range names {
		if slices.Contains(step.Routes, name) {
			return name
		}
	}
	return ""
}

func exposure(t *testing.T, f *evaluationFixture, arm string, key discoveryKey) ([]string, int) {
	t.Helper()
	specs := f.registration(arm, key).Specs
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, string(spec.Name))
	}
	facts, err := contextmeasure.Measure(&model.Request{Tools: modelTools(t, specs)}, model.ContextLimits{}, nil)
	require.NoError(t, err)
	return names, facts.FixedBytes
}
