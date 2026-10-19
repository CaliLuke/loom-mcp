package inspect

import (
	"context"
	"encoding/json/jsontext"
	"slices"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
)

// projectRecord applies mandatory structural exclusions before any host callback.
func projectRecord(record *runrecord.RunRecord, policy Policy) (*runrecord.Projection, error) {
	return record.Project(runrecord.ProjectionPolicy{Name: policy.Name, Content: func(c runrecord.ProjectionContent) (jsontext.Value, bool, error) {
		switch c.Kind {
		case "message", "tool_arguments", "tool_result", "typed_answer", "await_answer":
		default:
			return nil, false, nil
		}
		if string(c.Role) == "system" || string(c.Role) == "developer" || policy.RunContent == nil {
			return nil, false, nil
		}
		return policy.RunContent(c)
	}})
}

// canonical retains only explicit sharing DTOs, never raw exports or local reports.
func (b *publicationBuilder) canonical(ctx context.Context) error {
	records, incomplete, err := b.canonicalRecords(ctx)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		b.missing(RunView, "canonical", "canonical_evidence_missing", []Role{Editor, Evaluator})
		return nil
	}
	seen := map[string]bool{}
	for _, record := range records {
		if record == nil || seen[record.RootRunID] {
			continue
		}
		seen[record.RootRunID] = true
		for _, policy := range b.input.Policies {
			if policy.Role != Editor && policy.Role != Evaluator {
				continue
			}
			if err := b.addRecordViews(record, policy, incomplete); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *publicationBuilder) addCanonical(k Kind, name string, data []byte, role Role, provisional bool) error {
	// Same canonical key has one independently approved body per role.
	saved := b.entries
	b.entries = nil
	err := b.add(k, name, "application/json", data, bodyProvider{Kind: blobProvider, Name: string(role)}, []Role{role}, provisional)
	next := slices.Clone(b.entries)
	b.entries = saved
	b.entries = append(b.entries, next...)
	return err
}

func (b *publicationBuilder) canonicalRecords(ctx context.Context) ([]*runrecord.RunRecord, bool, error) {
	var records []*runrecord.RunRecord
	evidence, err := b.workspace.experiments.ReadEvidence(ctx, b.input.Origin.ExperimentID)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	incomplete := err != nil
	for _, e := range evidence {
		if e.Key == b.input.Origin.Key {
			if e.Error != "" {
				incomplete = true
			}
			if e.Snapshot != nil {
				records = append(records, e.Snapshot.Records...)
			}
		}
	}
	if len(records) == 0 && b.attempt != nil {
		for _, run := range b.attempt.Runs {
			record, err := run.Record()
			if err != nil {
				incomplete = true
				continue
			}
			records = append(records, record)
		}
	}
	return records, incomplete, nil
}

func (b *publicationBuilder) addRecordViews(record *runrecord.RunRecord, policy Policy, incomplete bool) error {
	projection, err := projectRecord(record, policy)
	if err != nil {
		return ErrInvalidInput
	}
	data, err := projection.Marshal()
	if err != nil {
		return ErrIntegrity
	}
	provisional := b.input.Provisional || b.attempt != nil && b.attempt.Probe != nil && b.attempt.Probe.Evidence == "provisional"
	key := entryKey(RunView, record.RootRunID)
	if err := b.addCanonical(RunView, record.RootRunID, data, policy.Role, provisional); err != nil {
		return err
	}
	last := &b.entries[len(b.entries)-1]
	last.entry.SourceDigest = projection.Source.ExportSHA256
	last.entry.RendererVersion = "loom-mcp.public-evidence.v1"
	incomplete = incomplete || projection.Incomplete || slices.ContainsFunc(projection.Runs, func(run runrecord.ProjectedRun) bool { return !run.Sealed })
	last.entry.Incomplete = incomplete
	last.entry.Provenance.RunID = record.RootRunID

	return b.addCallViews(projection, policy.Role, provisional, incomplete, key)
}

func (b *publicationBuilder) addCallViews(projection *runrecord.Projection, role Role, provisional, incomplete bool, key string) error {
	for _, run := range projection.Runs {
		for _, call := range run.Calls {
			data, err := encode(call)
			if err != nil {
				return ErrIntegrity
			}
			name := run.RunID + "-" + strconv.Itoa(call.Key.Seq) + "-" + strconv.Itoa(call.Key.Attempt)
			if err := b.addCanonical(CallView, name, data, role, provisional); err != nil {
				return err
			}
			last := &b.entries[len(b.entries)-1]
			last.sourceKey = key
			last.entry.SourceDigest = projection.Source.ExportSHA256
			last.entry.RendererVersion = "loom-mcp.public-evidence.v1"
			last.entry.Incomplete = incomplete
			last.entry.Provenance.RunID = run.RunID
			last.entry.Provenance.Step = call.Step
			last.entry.Provenance.CallSeq = call.Key.Seq
			last.entry.Provenance.CallAttempt = call.Key.Attempt
		}
	}
	return nil
}
