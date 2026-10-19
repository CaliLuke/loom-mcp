package control_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/controlcli"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlComparisonNativeGroups(t *testing.T) {
	cfg := config(t)
	jobs := &jobSet{jobs: map[string]control.JobRecord{}}
	jobs.jobs["a"] = savedProbe(t, cfg, "a")
	jobs.jobs["b"] = savedProbe(t, cfg, "b")
	cfg.Executor = jobs
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	reg := native(t, service(t, cfg))
	out, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: "experiment.compare_trials", ToolCallID: "groups", Payload: rawjson.Message(`{"baseline_job_ids":["a"],"candidate_job_ids":["b"]}`)})
	require.NoError(t, err)
	require.Nil(t, out.ToolResult.Error)
}

func TestControlComparisonGroupRequestParity(t *testing.T) {
	cfg := config(t)
	jobs := &jobSet{jobs: map[string]control.JobRecord{}}
	jobs.jobs["a"] = savedProbe(t, cfg, "a")
	jobs.jobs["b"] = savedProbe(t, cfg, "b")
	cfg.Executor = jobs
	cfg.Operations = append(cfg.Operations, control.CompareOperation)
	cfg.Limits.MaxPageEntries = 1
	for _, tt := range []struct {
		name  string
		input string
		valid bool
	}{
		{"scalar", `{"baseline_job_id":"a","candidate_job_id":"b"}`, true},
		{"groups_at_limit", `{"baseline_job_ids":["a"],"candidate_job_ids":["b"]}`, true},
		{"empty_group", `{"baseline_job_ids":[],"candidate_job_ids":["b"]}`, false},
		{"empty_with_scalar", `{"baseline_job_id":"a","candidate_job_id":"b","baseline_job_ids":[]}`, false},
		{"mixed", `{"baseline_job_id":"a","candidate_job_ids":["b"]}`, false},
		{"both", `{"baseline_job_id":"a","candidate_job_id":"b","baseline_job_ids":["a"],"candidate_job_ids":["b"]}`, false},
		{"above_limit", `{"baseline_job_ids":["a","c"],"candidate_job_ids":["b"]}`, false},
		{"repeated", `{"baseline_job_ids":["a","a"],"candidate_job_ids":["b"]}`, false},
		{"cross_side_duplicate", `{"baseline_job_ids":["a"],"candidate_job_ids":["a"]}`, false},
		{"missing_side", `{"baseline_job_ids":["a"]}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var expected control.Comparison
			for _, route := range []string{"Go", "native", "CLI"} {
				t.Run(route, func(t *testing.T) {
					got, err := comparisonRoute(t, cfg, route, tt.input)
					if !tt.valid {
						code(t, err, control.InvalidInput)
						return
					}
					require.NoError(t, err)
					if route == "Go" {
						expected = got
					}
					assert.Equal(t, expected, got)
					if tt.name == "scalar" {
						encoded, err := json.Marshal(got)
						require.NoError(t, err)
						assert.NotContains(t, string(encoded), "groups")
						assert.NotContains(t, string(encoded), "only_baseline")
					}
				})
			}
		})
	}
}

// comparisonRoute exercises the same store and validates the advertised result schema.
func comparisonRoute(t *testing.T, cfg control.Config, route, input string) (control.Comparison, error) {
	t.Helper()
	s := service(t, cfg)
	if route == "Go" {
		var q control.CompareRequest
		require.NoError(t, json.Unmarshal([]byte(input), &q))
		return s.Compare(t.Context(), q)
	}
	if route == "native" {
		reg := native(t, s)
		out, err := reg.Execute(t.Context(), &planner.ToolRequest{Name: "experiment.compare_trials", ToolCallID: "parity", Payload: rawjson.Message(input)})
		if err != nil {
			return control.Comparison{}, err
		}
		if out.ToolResult.Error != nil {
			var failure control.Error
			require.NoError(t, json.Unmarshal([]byte(out.ToolResult.Error.Message), &failure))
			return control.Comparison{}, &failure
		}
		for _, spec := range reg.Specs {
			if spec.Name == "experiment.compare_trials" {
				encoded, err := json.Marshal(out.ToolResult.Result)
				require.NoError(t, err)
				var value any
				require.NoError(t, json.Unmarshal(encoded, &value))
				require.NoError(t, tools.ValidateJSONSchema(spec.Result.Schema, value))
			}
		}
		return out.ToolResult.Result.(control.Comparison), nil
	}
	extensions, err := controlcli.Extensions(func(_ context.Context, store *eval.Store) (*control.Service, error) {
		cfg.Experiments = store
		return control.New(cfg)
	})
	require.NoError(t, err)
	var output, diagnostic bytes.Buffer
	exit := evalcli.Main(t.Context(), []string{"experiment", "compare_trials", "--request", input}, evalcli.Config{StoreDir: cfg.Experiments.Dir(), Extensions: extensions, Stdout: &output, Stderr: &diagnostic})
	if exit != 0 {
		var failure control.Error
		require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &failure))
		return control.Comparison{}, &failure
	}
	var result control.Comparison
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	return result, nil
}
