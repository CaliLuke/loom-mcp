package opik

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"net/http"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

type preparedAttempt struct {
	result    AttemptResult
	datasetID string
	trace     map[string]any
	feedback  []feedbackScore
}

// Export explicitly publishes a stored experiment through the selected policy.
// Remote failures return stable attempted identities with the error. Source
// evidence is never modified; the target manifest only records export identity.
func (c *Client) Export(ctx context.Context, experiment *eval.Experiment) (*Result, error) {
	return c.exportExperiment(ctx, experiment, nil)
}

func remoteExperimentStatus(status string) string {
	switch status {
	case statusRunning:
		return statusRunning
	case "stopped":
		return "cancelled"
	default:
		return "completed"
	}
}

func newExportResult(experiment *eval.Experiment, manifest *exportManifest) *Result {
	result := &Result{ExperimentID: manifest.id("experiment", ""), Incomplete: !experiment.Complete()}
	if !experiment.Complete() {
		result.Warnings = append(result.Warnings, "incomplete_experiment")
	}
	if experiment.TruncatedTail {
		result.Warnings = append(result.Warnings, "truncated_attempt_log")
	}
	if len(experiment.Attempts) < experiment.Header.Planned {
		result.Warnings = append(result.Warnings, "missing_attempt_rows")
	}
	return result
}

func (c *Client) exportExperiment(ctx context.Context, experiment *eval.Experiment, warnings map[eval.AttemptKey][]string) (*Result, error) {
	if experiment == nil || experiment.Dir == "" || experiment.Header.ID == "" {
		return nil, fmt.Errorf("opik: stored experiment identity and directory required")
	}
	if experiment.Header.Status != "complete" && experiment.Header.Status != statusRunning && experiment.Header.Status != "stopped" {
		return nil, fmt.Errorf("opik: invalid recorded experiment status %q", experiment.Header.Status)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := c.manifest(experiment)
	if err != nil {
		return nil, err
	}
	result := newExportResult(experiment, manifest)
	prepared, err := c.prepare(experiment, manifest, warnings)
	if err != nil {
		return result, err
	}
	for _, attempt := range prepared {
		result.Attempts = append(result.Attempts, attempt.result)
		result.Incomplete = result.Incomplete || attempt.result.Incomplete
	}
	datasetName := "loom-eval-" + experiment.Header.ID
	if err := c.dataset(ctx, datasetName, prepared); err != nil {
		return result, err
	}
	experimentBody := map[string]any{"id": result.ExperimentID, "name": experiment.Header.ID, fieldDataset: datasetName, fieldProject: c.config.Project, "status": remoteExperimentStatus(experiment.Header.Status), fieldMetadata: map[string]any{fieldExperiment: experiment.Header.ID, fieldPolicy: c.config.Policy.Name, fieldIncomplete: result.Incomplete, fieldWarnings: result.Warnings, "source_status": experiment.Header.Status, "source_stop": experiment.Header.Stop, "source_identity": eval.Identity{Source: experiment.Header.Source, Versions: experiment.Header.Versions}}}
	if err := c.ensure(ctx, "/experiments", result.ExperimentID, experimentBody, []string{"id", "status", fieldDataset, fieldProject, fieldMetadata}); err != nil {
		return result, err
	}
	for _, attempt := range prepared {
		if err := c.exportAttempt(ctx, result.ExperimentID, attempt); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (c *Client) dataset(ctx context.Context, name string, attempts []preparedAttempt) error {
	seen := make(map[string]bool)
	// Send single-item upserts so a process failure can safely retry each stable
	// dataset identity without an unbounded batch or an implicit server chunker.
	for _, attempt := range attempts {
		if seen[attempt.datasetID] {
			continue
		}
		seen[attempt.datasetID] = true
		key := attempt.result.Key
		body := map[string]any{fieldDataset: name, fieldProject: c.config.Project, "items": []any{map[string]any{"id": attempt.datasetID, "source": sourceSDK, "data": map[string]any{fieldScenario: key.Scenario, fieldItem: key.Item}}}}
		if _, _, err := c.request(ctx, http.MethodPut, "/datasets/items", body); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) exportAttempt(ctx context.Context, experimentID string, attempt preparedAttempt) error {
	if err := c.ensure(ctx, "/traces", attempt.result.TraceID, attempt.trace, []string{"id", fieldMetadata}); err != nil {
		return err
	}
	item := map[string]any{"id": attempt.result.ItemID, "experiment_id": experimentID, "dataset_item_id": attempt.datasetID, "trace_id": attempt.result.TraceID, fieldProject: c.config.Project}
	if err := c.ensure(ctx, "/experiments/items", attempt.result.ItemID, item, []string{"id", "experiment_id", "dataset_item_id", "trace_id"}); err != nil {
		return err
	}
	for _, score := range attempt.feedback {
		if _, _, err := c.request(ctx, http.MethodPut, "/traces/"+attempt.result.TraceID+"/feedback-scores", score); err != nil {
			return err
		}
	}
	return nil
}

func evidenceDigest(value any) (string, error) {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
