package eval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// ReadArtifact resolves an exact retained descriptor and verifies its owned bytes.
func (s *Store) ReadArtifact(ctx context.Context, experimentID string, key AttemptKey, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e, err := s.Load(experimentID)
	if err != nil {
		return nil, err
	}
	for _, attempt := range e.Attempts {
		if attempt.AttemptKey != key {
			continue
		}
		for _, artifact := range attempt.Artifacts {
			if artifact.Name != name {
				continue
			}
			f, err := os.OpenInRoot(s.dir, filepath.Join(experimentID, "evidence", strconv.Itoa(attempt.Index), "artifacts", artifact.Digest))
			if err != nil {
				return nil, err
			}
			data, readErr := io.ReadAll(f)
			err = errors.Join(readErr, f.Close())
			if err != nil {
				return nil, err
			}
			if int64(len(data)) != artifact.Size || artifactDigest(data) != artifact.Digest {
				return nil, errors.New("eval: retained artifact digest or size differs")
			}
			return data, ctx.Err()
		}
	}
	return nil, fmt.Errorf("eval: unknown retained artifact %q", name)
}

func artifactDigest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// validatePreparedPlan compares only recorded configuration, never opaque callbacks.
func (w *Writer) validatePreparedPlan(plan preparedPlan) error {
	if w == nil {
		return errors.New("eval: prepared execution requires PreparedWriter")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.finished || w.header.Version != 2 || w.header.ExecutionKind != plan.Kind || !slices.Equal(w.keys, plan.Keys) {
		return errors.New("eval: prepared writer does not match open plan")
	}
	want, err := encodeStore(preparedPlan{Kind: w.header.ExecutionKind, Keys: w.keys, Scenarios: w.header.Scenarios, Variants: w.header.Variants, Config: w.header.Config})
	if err != nil {
		return err
	}
	got, err := encodeStore(plan)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return errors.New("eval: prepared writer provenance or settings differ")
	}
	return nil
}

// reservePrepared publishes the exclusive canonical evidence binding before acquisition.
func (w *Writer) reservePrepared(key AttemptKey) (string, error) {
	if w == nil {
		return "", errors.New("eval: missing prepared writer")
	}
	w.mu.Lock()
	var dir string
	index, err := w.preparedIndex(key)
	if err == nil {
		dir, err = w.writeEvidenceBinding(index, key)
		if err == nil {
			if w.reserved == nil {
				w.reserved = make(map[int]bool)
			}
			w.reserved[index] = true
		}
	}
	notify := w.latch(err)
	w.mu.Unlock()
	if notify != nil {
		notify(err)
	}
	return dir, err
}

func (w *Writer) writePreparedReceipt(key AttemptKey, receipt PreparedReceipt) error {
	w.mu.Lock()
	index, err := w.reservedIndex(key)
	if err == nil {
		err = validatePreparedReceipt(receipt)
	}
	if err == nil {
		if _, exists := w.receipts[index]; exists {
			err = errors.New("eval: prepared receipt already published")
		}
	}
	if err == nil {
		receipt = *clonePreparedReceipt(&receipt)
		data, encodeErr := encodeStore(receipt)
		err = encodeErr
		if err == nil {
			err = appendFile(filepath.Join(w.preparedDir(index), "receipt.json"), data, true)
		}
		if err == nil {
			err = syncDirectory(w.preparedDir(index))
		}
		if err == nil {
			if w.receipts == nil {
				w.receipts = make(map[int]PreparedReceipt)
			}
			w.receipts[index] = receipt
		}
	}
	notify := w.latch(err)
	w.mu.Unlock()
	if notify != nil {
		notify(err)
	}
	return err
}

func (w *Writer) retainPreparedArtifacts(key AttemptKey, artifacts []CollectedArtifact, provisional bool) ([]StoredArtifact, error) {
	w.mu.Lock()
	index, err := w.reservedIndex(key)
	var retained []StoredArtifact
	if err == nil {
		retained, err = w.retainArtifacts(index, artifacts, provisional)
	}
	if err == nil {
		if w.artifacts == nil {
			w.artifacts = make(map[int][]StoredArtifact)
		}
		w.artifacts[index] = slices.Clone(retained)
	}
	notify := w.latch(err)
	w.mu.Unlock()
	if notify != nil {
		notify(err)
	}
	return retained, err
}

func (w *Writer) preparedIndex(key AttemptKey) (int, error) {
	if w.err != nil {
		return -1, w.err
	}
	index := slices.Index(w.keys, key)
	if w.finished || w.header.Version != 2 || index < 0 {
		return -1, errors.New("eval: prepared writer is finished or attempt differs")
	}
	return index, nil
}

func (w *Writer) reservedIndex(key AttemptKey) (int, error) {
	index, err := w.preparedIndex(key)
	if err == nil && !w.reserved[index] {
		err = errors.New("eval: prepared attempt was not reserved")
	}
	return index, err
}

func (w *Writer) preparedDir(index int) string {
	return filepath.Join(w.dir, "evidence", strconv.Itoa(index))
}

func (w *Writer) retainArtifacts(index int, artifacts []CollectedArtifact, provisional bool) ([]StoredArtifact, error) {
	seen := make(map[string]bool)
	var retained []StoredArtifact
	for _, artifact := range artifacts {
		if err := validateArtifactName(artifact.Name, artifact.MediaType); err != nil {
			return nil, err
		}
		if seen[artifact.Name] {
			return nil, errors.New("eval: duplicate collected artifact")
		}
		seen[artifact.Name] = true
	}
	dir := filepath.Join(w.preparedDir(index), "artifacts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := syncDirectory(w.preparedDir(index)); err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		digest := artifactDigest(artifact.Data)
		if err := appendFile(filepath.Join(dir, digest), artifact.Data, true); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
			f, openErr := os.OpenInRoot(dir, digest)
			if openErr != nil {
				return nil, openErr
			}
			data, readErr := io.ReadAll(f)
			readErr = errors.Join(readErr, f.Close())
			if readErr != nil {
				return nil, readErr
			}
			if !bytes.Equal(data, artifact.Data) {
				return nil, errors.New("eval: conflicting retained artifact blob")
			}
		}
		retained = append(retained, StoredArtifact{Name: artifact.Name, MediaType: artifact.MediaType, Digest: digest, Size: int64(len(artifact.Data)), Provisional: provisional})
	}
	return retained, syncDirectory(dir)
}

// validatePreparedRecord prevents callers from substituting a receipt or blob list.
func (w *Writer) validatePreparedRecord(r *AttemptRecord) error {
	if r.ExecutionKind != w.header.ExecutionKind {
		return errors.New("eval: attempt execution kind differs from writer")
	}
	if r.ExecutionKind == "" {
		return nil
	}
	if receipt, exists := w.receipts[r.Index]; exists {
		actual := clonePreparedReceipt(r.Prepared)
		if actual == nil {
			return errors.New("eval: attempt dropped its durable prepared receipt")
		}
		want, err := encodeStore(receipt)
		if err != nil {
			return err
		}
		got, err := encodeStore(actual)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return errors.New("eval: attempt receipt differs from durable prepared receipt")
		}
	} else if r.Prepared != nil {
		return errors.New("eval: attempt receipt was not durably published")
	}
	if !slices.Equal(r.Artifacts, w.artifacts[r.Index]) {
		return errors.New("eval: attempt artifacts differ from retained outputs")
	}
	return nil
}
