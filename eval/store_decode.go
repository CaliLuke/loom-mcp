package eval

import (
	"bufio"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
)

func decodeExperiment(data []byte, id string) (*ExperimentHeader, error) {
	var h ExperimentHeader
	if err := decodeStore(data, &h); err != nil {
		return nil, err
	}
	if h.Format != experimentFormat || validateExecutionKind(h.Version, h.ExecutionKind) != nil {
		return nil, fmt.Errorf("unsupported experiment format %q or version %d", h.Format, h.Version)
	}
	if h.Version == 1 {
		if err := rejectLegacyMembers(data, "execution_kind"); err != nil {
			return nil, err
		}
		var wire struct {
			Config jsontext.Value `json:"config"`
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, err
		}
		if len(wire.Config) != 0 {
			if err := rejectLegacyMembers(wire.Config, "prepare_timeout", "cleanup_timeout"); err != nil {
				return nil, err
			}
		}
	}
	if h.Version == 2 && (h.Config.PrepareTimeout <= 0 || h.Config.CleanupTimeout <= 0) {
		return nil, errors.New("eval: prepared header requires positive callback timeouts")
	}
	if h.ExecutionKind == ExecutionProbe && (h.Parent != "" || h.Judge != nil || h.Planned != 1) {
		return nil, errors.New("eval: probe header contains evaluation lineage or judge, or invalid plan")
	}
	if h.ID != id {
		return nil, fmt.Errorf("experiment ID mismatch: %q", h.ID)
	}
	switch h.Status {
	case statusRunning, statusComplete, statusStopped:
	default:
		return nil, fmt.Errorf("invalid experiment status %q", h.Status)
	}
	return &h, nil
}

// readAttempts tolerates only an incomplete final JSON value, never a corrupt complete line.
func readAttempts(r io.Reader, source string, header *ExperimentHeader) ([]*StoredAttempt, bool, error) {
	reader := bufio.NewReader(r)
	var attempts []*StoredAttempt
	seen := make(map[int]bool)
	keys := make(map[AttemptKey]bool)
	for line := 1; ; line++ {
		data, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, false, &DecodeError{Source: source, Line: line, Err: readErr}
		}
		if len(data) == 0 && line > 1 {
			break
		}
		var err error
		if line == 1 {
			err = decodeAttemptsHeader(data, header)
		} else {
			var a *StoredAttempt
			a, err = decodeUniqueAttempt(data, seen, keys)
			if err == nil && a.ExecutionKind != header.ExecutionKind {
				err = errors.New("eval: attempt execution kind differs from header")
			}
			if err == nil && header.Version == 1 {
				err = rejectLegacyMembers(data, "execution_kind", "prepared", "probe", "artifacts")
			}
			if err == nil {
				attempts = append(attempts, a)
			}
		}
		if err != nil {
			if line > 1 && errors.Is(readErr, io.EOF) && errors.Is(err, io.ErrUnexpectedEOF) {
				return sortedAttempts(attempts), true, nil
			}
			return nil, false, &DecodeError{Source: source, Line: line, Err: err}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return sortedAttempts(attempts), false, nil
}

func sortedAttempts(attempts []*StoredAttempt) []*StoredAttempt {
	slices.SortFunc(attempts, func(a, b *StoredAttempt) int {
		return a.Index - b.Index
	})
	return attempts
}

func decodeAttemptsHeader(data []byte, header *ExperimentHeader) error {
	var h attemptsHeader
	if err := decodeStore(data, &h); err != nil {
		return err
	}
	if h.Format != attemptsFormat || h.Version != header.Version || h.Experiment != header.ID {
		return fmt.Errorf("missing or mismatched attempts header")
	}
	return nil
}

func decodeUniqueAttempt(data []byte, seen map[int]bool, keys map[AttemptKey]bool) (*StoredAttempt, error) {
	a, err := decodeAttemptLine(data)
	if err != nil {
		return nil, err
	}
	if seen[a.Index] || keys[a.AttemptKey] {
		return nil, fmt.Errorf("duplicate attempt index or key")
	}
	seen[a.Index], keys[a.AttemptKey] = true, true
	return a, nil
}

// DecodeExperimentHeader strictly decodes a persisted header without opening or
// creating a store. Offline inventory tools use the same format and nested wire
// validation as Store.Load, including the expected experiment ID.
func DecodeExperimentHeader(data []byte, id string) (*ExperimentHeader, error) {
	return decodeExperiment(data, id)
}
