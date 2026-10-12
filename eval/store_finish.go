package eval

import (
	"errors"
	"os"
	"path/filepath"
)

// replaceAttempts publishes final records only after the complete replacement is
// synced and closed. Readers keep the streamed prefix if preparing it fails.
func (w *Writer) replaceAttempts(report *Report) (err error) {
	file, err := os.CreateTemp(w.dir, ".attempts.jsonl.tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	fingerprints, writeErr := w.writeAttempts(file, report)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	if w.store.beforeRename != nil {
		if err := w.store.beforeRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(file.Name(), filepath.Join(w.dir, "attempts.jsonl")); err != nil {
		return err
	}
	if err := syncDirectory(w.dir); err != nil {
		return err
	}
	w.written = fingerprints
	return nil
}

// writeAttempts streams the final log without retaining its encoded records in memory.
func (w *Writer) writeAttempts(file *os.File, report *Report) (map[int]Fingerprint, error) {
	header, err := encodeStore(attemptsHeader{Format: attemptsFormat, Version: 1, Experiment: w.ID()})
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(append(header, '\n')); err != nil {
		return nil, err
	}
	fingerprints := make(map[int]Fingerprint, len(report.Attempts))
	for _, a := range w.finalAttempts(report) {
		data, fingerprint, err := w.encodeAttempt(a)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return nil, err
		}
		fingerprints[a.Index] = fingerprint
	}
	return fingerprints, nil
}

// finalAttempts preserves durable completion order, then adds missing records in
// report order. The validated report supplies every final value by stable index.
func (w *Writer) finalAttempts(report *Report) []*AttemptRecord {
	byIndex := make([]*AttemptRecord, len(report.Attempts))
	for _, a := range report.Attempts {
		byIndex[a.Index] = a
	}
	ordered := make([]*AttemptRecord, 0, len(report.Attempts))
	for _, index := range w.order {
		ordered = append(ordered, byIndex[index])
	}
	for _, a := range report.Attempts {
		if _, exists := w.written[a.Index]; !exists {
			ordered = append(ordered, a)
		}
	}
	return ordered
}
