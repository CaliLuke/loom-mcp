package runrecord

import (
	"context"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// exportEvidenceSource exposes only the content and canonical records included
// in this export. Verification cannot fall back to a backend or live store.
type exportEvidenceSource struct{ record *RunRecord }

func (s exportEvidenceSource) Get(_ context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	if scope != s.record.RootRunID {
		return nil, fmt.Errorf("export compatibility scope mismatch")
	}
	data, ok := s.record.Content[hash]
	if !ok {
		return nil, fmt.Errorf("export compatibility content %s: %w", hash, runstore.ErrNotFound)
	}
	return data, nil
}
func (s exportEvidenceSource) ListCalls(_ context.Context, scope, runID string) ([]runstore.CallRecord, error) {
	if scope != s.record.RootRunID {
		return nil, fmt.Errorf("export compatibility scope mismatch")
	}
	for _, run := range s.record.Runs {
		if run.RunID == runID {
			records := make([]runstore.CallRecord, 0, len(run.Calls))
			for _, call := range run.Calls {
				records = append(records, call.Record)
			}
			return records, nil
		}
	}
	return nil, fmt.Errorf("export compatibility run %s missing", runID)
}
func (s exportEvidenceSource) List(_ context.Context, runID, cursor string, _ int) (runlog.Page, error) {
	if cursor != "" {
		return runlog.Page{}, fmt.Errorf("unexpected export event cursor")
	}
	for _, run := range s.record.Runs {
		if run.RunID == runID {
			return runlog.Page{Events: run.Events}, nil
		}
	}
	return runlog.Page{}, fmt.Errorf("export compatibility run %s missing", runID)
}
func (exportEvidenceSource) Put(context.Context, string, []byte) (runstore.Hash, error) {
	return "", fmt.Errorf("export evidence is read-only")
}
func (exportEvidenceSource) NextSeq(context.Context, string, string) (int, error) {
	return 0, fmt.Errorf("export evidence is read-only")
}
func (exportEvidenceSource) PutCall(context.Context, string, runstore.CallRecord) error {
	return fmt.Errorf("export evidence is read-only")
}
func (exportEvidenceSource) DeleteScope(context.Context, string) error {
	return fmt.Errorf("export evidence is read-only")
}
func (exportEvidenceSource) Append(context.Context, *runlog.Event) (runlog.AppendResult, error) {
	return runlog.AppendResult{}, fmt.Errorf("export evidence is read-only")
}

// verifyExportCompatibility replays only canonical records for runs using the
// new attachment proof. Older exports retain their historical decoding path.
func verifyExportCompatibility(record *RunRecord) error {
	source := exportEvidenceSource{record: record}
	for _, run := range record.Runs {
		check := run.WorkflowProtocolVersion >= 19 && run.ContextSettings != nil && run.ContextSettings.KeepNonTextResident
		for _, event := range run.Events {
			check = check || event.Type == hooks.ContextCompatibility || event.Type == hooks.ContextNonTextResident || event.Type == hooks.ContextResidencyAdopted
		}
		if !check {
			continue
		}
		rebuilt, err := Read(context.Background(), source, source, record.RootRunID, run.RunID)
		if err != nil {
			return fmt.Errorf("verify exported compatibility: %w", err)
		}
		i := slices.IndexFunc(rebuilt.Runs, func(r Run) bool {
			return r.RunID == run.RunID
		})
		if i < 0 || rebuilt.Runs[i].NonTextResident != run.NonTextResident {
			return fmt.Errorf("exported non-text residency differs from canonical adoption")
		}
	}
	return nil
}
