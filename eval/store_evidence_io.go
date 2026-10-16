package eval

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
)

// readEvidencePlan checks the independent capture index against its experiment.
func readEvidencePlan(dir string, header *ExperimentHeader) (evidencePlan, error) {
	var plan evidencePlan
	if err := readEvidenceJSON(filepath.Join(dir, "plan.json"), &plan); err != nil {
		return plan, fmt.Errorf("eval: read evidence plan: %w", err)
	}
	if plan.Version != 1 || plan.Experiment != header.ID || len(plan.Keys) != header.Planned {
		return plan, errors.New("eval: evidence plan differs from experiment")
	}
	return plan, nil
}

// readEvidenceEntry retains a planned identity even if capture cannot be opened.
func readEvidenceEntry(ctx context.Context, dir, name string, plan evidencePlan) (*AttemptEvidence, error) {
	index, err := strconv.Atoi(name)
	if err != nil || index < 0 || index >= len(plan.Keys) || strconv.Itoa(index) != name {
		return nil, fmt.Errorf("eval: invalid evidence directory %q", name)
	}
	result := &AttemptEvidence{Index: index, Key: plan.Keys[index]}
	if err := readBoundEvidence(ctx, filepath.Join(dir, name), plan.Experiment, result); err != nil {
		result.Error = err.Error()
		return result, fmt.Errorf("evidence attempt %d: %w", index, err)
	}
	return result, nil
}

func readBoundEvidence(ctx context.Context, dir, experiment string, result *AttemptEvidence) error {
	var binding evidenceBinding
	if err := readEvidenceJSON(filepath.Join(dir, "binding.json"), &binding); err != nil {
		return err
	}
	if binding.Version != 1 || binding.Experiment != experiment || binding.Index != result.Index || binding.Key != result.Key {
		return errors.New("eval: evidence binding differs from planned attempt")
	}
	snapshot, err := agenttest.ReadEvidence(ctx, filepath.Join(dir, "capture"))
	result.Snapshot = snapshot
	return err
}

func readEvidenceJSON(path string, value any) error {
	f, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(f)
	if err := errors.Join(readErr, f.Close()); err != nil {
		return err
	}
	return json.Unmarshal(data, value, json.RejectUnknownMembers(true))
}

// writeEvidenceJSON publishes only fully synced bindings; orphan temp files cannot
// represent a successful capture. A published directory without binding is an error.
func writeEvidenceJSON(dir, name string, value any) error {
	data, err := encodeStore(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".evidence.tmp-*")
	if err != nil {
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("publish evidence binding: %w", err)
	}
	return syncDirectory(dir)
}
