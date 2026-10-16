package opik

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

type feedbackScore struct {
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	Category string  `json:"category_name,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	Source   string  `json:"source"`
}

func judgmentIdentity(judgments []eval.StoredJudgment) []map[string]any {
	identities := make([]map[string]any, 0, len(judgments))
	for _, judgment := range judgments {
		identities = append(identities, map[string]any{"claim_id": judgment.ClaimID, "subject": judgment.Subject, "step": judgment.Step, "label": judgment.Label, "informational": judgment.Informational, "uncalibrated": judgment.Uncalibrated, "truncated": judgment.Truncated, "error": judgment.Error != ""})
	}
	return identities
}

// prepare freezes policy-selected content before any remote mutation.
func (c *Client) prepare(experiment *eval.Experiment, manifest *exportManifest, warnings map[eval.AttemptKey][]string) ([]preparedAttempt, error) {
	var prepared []preparedAttempt
	for _, attempt := range experiment.Attempts {
		if attempt == nil {
			return nil, fmt.Errorf("opik: nil stored attempt")
		}
		value, err := c.prepareAttempt(experiment, manifest, attempt, warnings[attempt.AttemptKey])
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, value)
	}
	return prepared, nil
}

func (c *Client) prepareAttempt(experiment *eval.Experiment, manifest *exportManifest, attempt *eval.StoredAttempt, extraWarnings []string) (preparedAttempt, error) {
	key := attempt.AttemptKey.String()
	out := preparedAttempt{result: AttemptResult{Key: attempt.AttemptKey, TraceID: manifest.id("trace", key), ItemID: manifest.id("experiment-item", key)}, datasetID: manifest.id("dataset-item", attempt.Scenario+"/"+attempt.Item)}
	projections, traceIDs, warnings, err := c.projectRuns(attempt)
	if err != nil {
		return out, err
	}
	warnings = append(warnings, extraWarnings...)
	out.result.Warnings = warnings
	out.result.Incomplete = len(warnings) != 0
	evaluation, err := c.selectContent("evaluation", "attempts/"+key, map[string]any{"task": attempt.Task, "reference": attempt.Reference, "claims": attempt.Claims, "judgments": attempt.Judgments, "checks": attempt.Checks, "scores": attempt.Scores, "error": attempt.Error, "harness_error": attempt.HarnessError})
	if err != nil {
		return out, err
	}
	evidence := map[string]any{"records": projections, "evaluation": evaluation}
	digest, err := evidenceDigest(evidence)
	if err != nil {
		return out, err
	}
	metadata := c.attemptMetadata(experiment, attempt, out.result, digest, traceIDs)
	started := attempt.Started
	if started.IsZero() {
		started = experiment.Header.Started
	}
	if started.IsZero() {
		return out, fmt.Errorf("opik: attempt %s has no recorded start time", key)
	}
	out.trace = map[string]any{"id": out.result.TraceID, fieldProject: c.config.Project, "name": key, "start_time": started, "input": map[string]any{fieldScenario: attempt.Scenario, fieldItem: attempt.Item, "variant": attempt.Variant, "repeat": attempt.Repeat}, "output": evidence, fieldMetadata: metadata, "source": "experiment"}
	if attempt.Duration > 0 {
		out.trace["end_time"] = started.Add(attempt.Duration)
	}
	out.feedback, err = c.feedback(attempt)
	return out, err
}

func (c *Client) attemptMetadata(experiment *eval.Experiment, attempt *eval.StoredAttempt, result AttemptResult, digest string, traceIDs []string) map[string]any {
	key := attempt.AttemptKey.String()
	metadata := map[string]any{fieldExperiment: experiment.Header.ID, "source_attempt": key, fieldPolicy: c.config.Policy.Name, "evidence_sha256": digest, fieldScenario: attempt.Scenario, fieldItem: attempt.Item, "variant": attempt.Variant, "repeat": attempt.Repeat, "verdict": attempt.Verdict, "not_run": attempt.NotRun, fieldIncomplete: result.Incomplete, fieldWarnings: result.Warnings, "otel_trace_ids": traceIDs, "source_fingerprint": attempt.Fingerprint, "source_identity": eval.Identity{Source: experiment.Header.Source, Versions: experiment.Header.Versions}, "judge": experiment.Header.Judge, "judgments": judgmentIdentity(attempt.Judgments)}
	for _, scenario := range experiment.Header.Scenarios {
		if scenario.ID == attempt.Scenario {
			metadata["agent"] = scenario.Agent
			metadata["dataset_hash"] = scenario.DatasetHash
			break
		}
	}
	return metadata
}

// projectRuns verifies raw evidence and preserves incomplete lifecycle warnings.
func (c *Client) projectRuns(attempt *eval.StoredAttempt) ([]*runrecord.Projection, []string, []string, error) {
	var projections []*runrecord.Projection
	var traces, warnings []string
	if len(attempt.Runs) == 0 {
		warnings = append(warnings, "missing_run_evidence")
	}
	if attempt.StorageError != "" {
		warnings = append(warnings, "storage_error")
	}
	for _, run := range attempt.Runs {
		if len(run.Export) == 0 {
			warnings = append(warnings, "missing_run_evidence")
			continue
		}
		record, err := run.Record()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("opik run %s: %w", run.RunID, err)
		}
		projection, err := record.Project(c.config.Policy)
		if err != nil {
			return nil, nil, nil, err
		}
		if projection.Incomplete {
			return nil, nil, nil, fmt.Errorf("opik: incomplete projection for %s", run.RunID)
		}
		projections = append(projections, projection)
		if run.TraceID != "" {
			traces = append(traces, run.TraceID)
		}
		if run.EventsUnverified {
			warnings = append(warnings, "events_unverified")
		}
		for _, child := range record.Runs {
			if !child.Sealed {
				warnings = append(warnings, "unsealed_run")
			}
			for _, call := range child.Calls {
				if call.Class == runrecord.Incomplete {
					warnings = append(warnings, "incomplete_call")
				}
			}
		}
	}
	return projections, traces, warnings, nil
}

// feedback maps checks and claim labels to scores while retaining their categories.
func (c *Client) feedback(attempt *eval.StoredAttempt) ([]feedbackScore, error) {
	var scores []feedbackScore
	for _, check := range attempt.Checks {
		value := 0.0
		if check.Passed {
			value = 1
		}
		reason, err := c.reason(attempt, "check/"+check.Name, check.Diagnostic)
		if err != nil {
			return nil, err
		}
		scores = append(scores, feedbackScore{Name: "check/" + check.Name, Value: value, Reason: reason, Source: sourceSDK})
	}
	for _, score := range attempt.Scores {
		scores = append(scores, feedbackScore{Name: "score/" + score.Name, Value: score.Value, Source: sourceSDK})
	}
	for i, judgment := range attempt.Judgments {
		if judgment.Error != "" {
			continue
		}
		value := 0.0
		if judgment.Label == string(eval.Entailed) {
			value = 1
		}
		name := "judgment/" + judgment.ClaimID + "/" + strconv.Itoa(i)
		reason, err := c.reason(attempt, name, judgment.Rationale)
		if err != nil {
			return nil, err
		}
		scores = append(scores, feedbackScore{Name: name, Value: value, Category: judgment.Label, Reason: reason, Source: sourceSDK})
	}
	return scores, nil
}

func (c *Client) reason(attempt *eval.StoredAttempt, name, text string) (string, error) {
	if text == "" {
		return "", nil
	}
	value, err := c.selectContent("feedback_reason", "attempts/"+attempt.AttemptKey.String()+"/"+name, text)
	if err != nil || value == nil {
		return "", err
	}
	var reason string
	if err := json.Unmarshal(value, &reason); err != nil {
		return "", fmt.Errorf("opik: feedback policy must return a JSON string: %w", err)
	}
	return reason, nil
}

// selectContent applies the same explicit app policy to evaluation text outside
// run records. An omission stays absent; malformed approved JSON fails closed.
func (c *Client) selectContent(kind, path string, source any) (jsontext.Value, error) {
	data, err := json.Marshal(source, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	value, include, err := c.config.Policy.Content(runrecord.ProjectionContent{Kind: kind, Path: path, Value: data})
	if err != nil {
		return nil, err
	}
	if !include {
		return nil, nil
	}
	if !value.IsValid() {
		return nil, fmt.Errorf("opik: invalid policy JSON at %s", path)
	}
	return append(jsontext.Value(nil), value...), nil
}
