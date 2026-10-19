package eval

import (
	"fmt"
	"path/filepath"
	"sync"
)

// Writer streams one experiment, retaining the first persistence failure.
// Methods are concurrency-safe; callbacks run after releasing the writer lock.
type Writer struct {
	reserved  map[int]bool
	receipts  map[int]PreparedReceipt
	artifacts map[int][]StoredArtifact
	mu        sync.Mutex
	store     *Store
	dir       string
	header    ExperimentHeader
	keys      []AttemptKey
	written   map[int]Fingerprint
	order     []int
	err       error
	finished  bool
}

// ID returns the experiment's canonical UUIDv7.
func (w *Writer) ID() string {
	return filepath.Base(w.dir)
}

// Append durably stores one planned attempt. Duplicate indices are rejected.
func (w *Writer) Append(r *AttemptRecord) error {
	w.mu.Lock()
	if err := w.validateAppend(r); err != nil {
		w.mu.Unlock()
		return err
	}
	err := w.append(r)
	notify := w.latch(err)
	w.mu.Unlock()
	if notify != nil {
		notify(err)
	}
	return err
}

// OnAttempt persists first and invokes next even when persistence fails.
func (w *Writer) OnAttempt(next func(*AttemptRecord)) func(*AttemptRecord) {
	return func(r *AttemptRecord) {
		// Append records its error in Err and reports it through WithOnError.
		if err := w.Append(r); err != nil {
			if next != nil {
				next(r)
			}
			return
		}
		if next != nil {
			next(r)
		}
	}
}

// Err returns the first persistence failure, if any.
func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// SetJudge supplies optional judge provenance for Finish.
func (w *Writer) SetJudge(j JudgeInfo) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.header.Judge = &j
}

// Finish atomically replaces attempts from the final report, then commits the summary.
func (w *Writer) Finish(report *Report, runErr error) error {
	w.mu.Lock()
	if err := w.validateReport(report); err != nil {
		w.mu.Unlock()
		return err
	}
	err := w.finish(report, runErr)
	notify := w.latch(err)
	w.mu.Unlock()
	if notify != nil {
		notify(err)
	}
	return err
}

func (w *Writer) append(r *AttemptRecord) error {
	data, fingerprint, err := w.encodeAttempt(r)
	if err != nil {
		return err
	}
	if err := appendFile(filepath.Join(w.dir, "attempts.jsonl"), append(data, '\n'), false); err != nil {
		return err
	}
	w.written[r.Index] = fingerprint
	w.order = append(w.order, r.Index)
	return nil
}

// encodeAttempt applies the same record-size policy to streaming and final writes.
func (w *Writer) encodeAttempt(r *AttemptRecord) ([]byte, Fingerprint, error) {
	a, err := storedAttempt(r)
	if err != nil {
		return nil, Fingerprint{}, err
	}
	if err := validateStoredPrepared(a); err != nil {
		return nil, Fingerprint{}, err
	}
	data, err := encodeStore(a)
	if err != nil {
		return nil, Fingerprint{}, err
	}
	if len(data) > w.store.options.maxRecordBytes {
		for i := range a.Runs {
			a.Runs[i].Export = nil
		}
		a.StorageError = fmt.Sprintf("record exceeds %d bytes; exports omitted", w.store.options.maxRecordBytes)
		data, err = encodeStore(a)
		if err != nil {
			return nil, Fingerprint{}, err
		}
	}
	return data, a.Fingerprint, nil
}

func (w *Writer) finish(report *Report, runErr error) error {
	if err := w.replaceAttempts(report); err != nil {
		return err
	}
	h := w.header
	h.Finished, h.Stop, h.Tokens, h.RunError = report.Finished, report.Stop, report.Tokens, errorText(runErr)
	h.Fingerprints = make(map[string]Fingerprint)
	for index, fingerprint := range w.written {
		variant := w.keys[index].Variant
		h.Fingerprints[variant] = mergeFingerprint(h.Fingerprints[variant], fingerprint)
	}
	h.Status = statusComplete
	if report.Stop != "" || runErr != nil {
		h.Status = statusStopped
	}
	if err := w.store.writeHeader(w.dir, h); err != nil {
		return err
	}
	w.header = h
	w.finished = true
	return nil
}

// latch schedules the user callback once, outside the mutex.
func (w *Writer) latch(err error) func(error) {
	if err == nil || w.err != nil {
		return nil
	}
	w.err = err
	return w.store.options.onError
}

func (w *Writer) validateAppend(r *AttemptRecord) error {
	if w.err != nil {
		return w.err
	}
	if w.finished {
		return fmt.Errorf("eval: writer is finished")
	}
	if r == nil || r.Index < 0 || r.Index >= len(w.keys) || w.keys[r.Index] != r.AttemptKey {
		return fmt.Errorf("eval: attempt does not match plan")
	}
	if _, ok := w.written[r.Index]; ok {
		return fmt.Errorf("eval: duplicate attempt index %d", r.Index)
	}

	return w.validatePreparedRecord(r)
}

func (w *Writer) validateReport(report *Report) error {
	if w.err != nil {
		return w.err
	}
	if w.finished {
		return fmt.Errorf("eval: writer is finished")
	}
	if report == nil || report.ExecutionKind != w.header.ExecutionKind || len(report.Attempts) != len(w.keys) {
		return fmt.Errorf("eval: report keys do not match plan")
	}
	seen := make(map[int]bool, len(w.keys))
	for _, a := range report.Attempts {
		if a == nil || a.Index < 0 || a.Index >= len(w.keys) || seen[a.Index] || w.keys[a.Index] != a.AttemptKey {
			return fmt.Errorf("eval: report keys do not match plan")
		}
		if err := w.validatePreparedRecord(a); err != nil {
			return err
		}
		seen[a.Index] = true
	}

	return nil
}
