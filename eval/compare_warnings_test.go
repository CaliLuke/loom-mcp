package eval

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(*Experiment)
		want       bool
	}{
		{"dataset", "dataset_changed", func(e *Experiment) {
			e.Header.Scenarios[0].DatasetHash = "changed"
		}, true},
		{"identical", "no_agent_change", func(*Experiment) {
		}, true},
		{"system", "no_agent_change", func(e *Experiment) {
			f := e.Header.Fingerprints["base"]
			f.SystemHashes = []string{"changed"}
			e.Header.Fingerprints["base"] = f
		}, false},
		{"tools", "no_agent_change", func(e *Experiment) {
			f := e.Header.Fingerprints["base"]
			f.ToolHashes = []string{"changed"}
			e.Header.Fingerprints["base"] = f
		}, false},
		{"context", "no_agent_change", func(e *Experiment) {
			f := e.Header.Fingerprints["base"]
			f.Context = []string{"changed"}
			e.Header.Fingerprints["base"] = f
		}, false},
		{"prompts", "no_agent_change", func(e *Experiment) {
			f := e.Header.Fingerprints["base"]
			f.Prompts = []prompt.PromptRef{{ID: "prompt", Version: "2"}}
			e.Header.Fingerprints["base"] = f
		}, false},
		{"models", "no_agent_change", func(e *Experiment) {
			f := e.Header.Fingerprints["base"]
			f.Models = map[model.ModelClass][]string{model.ModelClass("planner"): {"other"}}
			e.Header.Fingerprints["base"] = f
		}, false},
		{"policy", "no_agent_change", func(e *Experiment) {
			e.Header.Variants[0].Policy = &api.PolicyOverrides{}
		}, false},
		{"judge model", "judge_changed", func(e *Experiment) {
			e.Header.Judge.ModelID = "other"
		}, true},
		{"judge prompt", "judge_changed", func(e *Experiment) {
			e.Header.Judge.PromptHash = "other"
		}, true},
		{"judge absent", "judge_changed", func(e *Experiment) {
			e.Header.Judge = nil
		}, true},
		{"calibration", "judge_uncalibrated", func(e *Experiment) {
			e.Header.Judge.Calibrated = false
		}, true},
		{"judgment calibration", "judge_uncalibrated", func(e *Experiment) {
			e.Attempts[0].Judgments = []StoredJudgment{{Uncalibrated: true}}
		}, true},
		{"truncated", "judgments_truncated", func(e *Experiment) {
			e.Attempts[0].Judgments = []StoredJudgment{{Truncated: true}}
		}, true},
		{"running", "incomplete", func(e *Experiment) {
			e.Header.Status = statusRunning
		}, true},
		{"torn tail", "incomplete", func(e *Experiment) {
			e.TruncatedTail = true
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cand := comparisonExperiment("baseline", "base", "one"), comparisonExperiment("candidate", "base", "one")
			base.Header.Judge = &JudgeInfo{ModelID: "judge", PromptHash: "hash", Calibrated: true}
			cand.Header.Judge = &JudgeInfo{ModelID: "judge", PromptHash: "hash", Calibrated: true}
			tc.mutate(cand)
			c, err := Compare(Side{Experiment: base}, Side{Experiment: cand}, CompareOptions{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, countWarnings(c, tc.code) > 0)
		})
	}
}
