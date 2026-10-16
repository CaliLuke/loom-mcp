package opik

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// ExportStored loads final attempt rows and durable pre-dispatch captures. It
// includes interrupted attempts even when no completion line exists. Read errors
// remain errors alongside any successfully published, visibly partial evidence.
func (c *Client) ExportStored(ctx context.Context, store *eval.Store, ref string) (*Result, error) {
	if store == nil {
		return nil, fmt.Errorf("opik: local store required")
	}
	id, err := store.Resolve(ref)
	if err != nil {
		return nil, err
	}
	experiment, err := store.Load(id)
	if err != nil {
		return nil, err
	}
	rows, readErr := store.ReadEvidence(ctx, id)
	if readErr != nil && len(rows) == 0 {
		return nil, readErr
	}
	merged, warnings, err := mergeEvidence(experiment, rows)
	if err != nil {
		return nil, errors.Join(readErr, err)
	}
	result, exportErr := c.exportExperiment(ctx, merged, warnings)
	return result, errors.Join(readErr, exportErr)
}

// mergeEvidence preserves final verdicts and treats unfinished captures as pending.
func mergeEvidence(source *eval.Experiment, rows []eval.AttemptEvidence) (*eval.Experiment, map[eval.AttemptKey][]string, error) {
	merged := *source
	merged.Attempts = make([]*eval.StoredAttempt, 0, len(source.Attempts)+len(rows))
	byKey := make(map[eval.AttemptKey]*eval.StoredAttempt)
	warnings := make(map[eval.AttemptKey][]string)
	for _, attempt := range source.Attempts {
		if attempt == nil {
			return nil, nil, fmt.Errorf("opik: nil stored attempt")
		}
		copyAttempt := *attempt
		copyAttempt.Runs = slices.Clone(attempt.Runs)
		merged.Attempts = append(merged.Attempts, &copyAttempt)
		byKey[attempt.AttemptKey] = &copyAttempt
	}
	for _, row := range rows {
		attempt := byKey[row.Key]
		if attempt == nil {
			attempt = &eval.StoredAttempt{AttemptKey: row.Key, Index: row.Index, Key: row.Key.String(), Started: source.Header.Started, Verdict: eval.VerdictPending}
			merged.Attempts = append(merged.Attempts, attempt)
			byKey[row.Key] = attempt
			warnings[row.Key] = append(warnings[row.Key], "missing_attempt_completion")
		} else if attempt.Index != row.Index {
			return nil, nil, fmt.Errorf("opik: capture attempt identity differs from final row")
		}
		if row.Error != "" {
			warnings[row.Key] = append(warnings[row.Key], "capture_read_error")
		}
		if row.Snapshot == nil {
			continue
		}
		if err := appendRecords(attempt, row.Snapshot.Records, row.Error == ""); err != nil {
			return nil, nil, err
		}
		if len(row.Snapshot.Unlinked) != 0 {
			warnings[row.Key] = append(warnings[row.Key], "unlinked_run_evidence")
			if err := appendRecords(attempt, row.Snapshot.Unlinked, row.Error == ""); err != nil {
				return nil, nil, err
			}
		}
	}
	slices.SortFunc(merged.Attempts, func(a, b *eval.StoredAttempt) int { return a.Index - b.Index })
	return &merged, warnings, nil
}

// appendRecords prefers successful canonical captures while retaining final correlations.
func appendRecords(attempt *eval.StoredAttempt, records []*runrecord.RunRecord, replace bool) error {
	seen := make(map[string]int)
	for i, run := range attempt.Runs {
		seen[run.RunID] = i
	}
	for _, record := range records {
		if record == nil || len(record.Runs) == 0 {
			return fmt.Errorf("opik: empty captured record")
		}
		runID := record.Runs[0].RunID
		if _, exists := seen[runID]; exists && !replace {
			continue
		}
		raw, err := record.Export().Marshal()
		if err != nil {
			return err
		}
		if i, exists := seen[runID]; exists {
			attempt.Runs[i].Export = raw
		} else {
			seen[runID] = len(attempt.Runs)
			attempt.Runs = append(attempt.Runs, eval.StoredRun{RunID: runID, Export: raw})
		}
	}
	return nil
}
