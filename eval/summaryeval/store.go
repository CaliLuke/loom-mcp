package summaryeval

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
)

type logHeader struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Replay  string `json:"replay"`
}

type writer struct {
	dir  string
	file *os.File
}

const replayFormat = "loom-mcp.eval.compaction"

const cutsFormat = "loom-mcp.eval.compaction.cuts"

// List returns replay manifests newest first, ignoring the reserved cache directory.
func List(root string) ([]Header, error) {
	entries, err := os.ReadDir(filepath.Join(root, "_compaction"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var headers []Header
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		h, err := readHeader(root, entry.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		headers = append(headers, h)
	}
	slices.SortFunc(headers, func(a, b Header) int {
		if n := b.Started.Compare(a.Started); n != 0 {
			return n
		}
		return strings.Compare(b.ID, a.ID)
	})
	return headers, errors.Join(errs...)
}

// Resolve selects an exact ID, unique prefix of eight or more bytes, or last terminal replay.
func Resolve(root, ref string) (string, error) {
	headers, err := List(root)
	if err != nil {
		return "", err
	}
	if ref != "last" && len(ref) < 8 {
		return "", fmt.Errorf("%w: replay prefix must contain at least 8 characters", ErrUsage)
	}
	found := ""
	for _, h := range headers {
		if ref == "last" {
			if h.Status != statusRunning {
				return h.ID, nil
			}
			continue
		}
		if strings.HasPrefix(h.ID, ref) {
			if found != "" {
				return "", fmt.Errorf("%w: ambiguous replay %q", ErrUsage, ref)
			}
			found = h.ID
		}
	}
	if found == "" {
		return "", fmt.Errorf("%w: unknown replay %q", ErrUsage, ref)
	}
	return found, nil
}

// Load strictly decodes complete lines; an unterminated final line is a torn tail.
func Load(root, id string) (*Experiment, error) {
	h, err := readHeader(root, id)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenInRoot(filepath.Join(root, "_compaction"), filepath.Join(id, "cuts.jsonl"))
	if err != nil {
		return nil, err
	}
	result, readErr := readLines(f, h)
	return result, errors.Join(readErr, f.Close())
}

func readHeader(root, id string) (Header, error) {
	var h Header
	if !validID(id) {
		return h, fmt.Errorf("invalid replay ID")
	}
	f, err := os.OpenInRoot(filepath.Join(root, "_compaction"), filepath.Join(id, "replay.json"))
	if err != nil {
		return h, err
	}
	data, readErr := io.ReadAll(f)
	if err := errors.Join(readErr, f.Close()); err != nil {
		return h, err
	}
	if err := decode(data, &h); err != nil {
		return h, err
	}
	if h.Format != replayFormat || h.Version != 1 || h.ID != id || (h.Status != statusRunning && h.Status != statusComplete && h.Status != statusStopped) {
		return h, fmt.Errorf("unsupported compaction replay header")
	}
	return h, nil
}

func readLines(input io.Reader, h Header) (*Experiment, error) {
	r := bufio.NewReader(input)
	data, err := r.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("compaction cuts header: %w", err)
	}
	var header logHeader
	if err := decode(bytes.TrimSuffix(data, []byte{'\n'}), &header); err != nil {
		return nil, err
	}
	if header.Format != cutsFormat || header.Version != 1 || header.Replay != h.ID {
		return nil, fmt.Errorf("unsupported compaction cuts header")
	}
	result := &Experiment{Header: h}
	for {
		data, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			result.TruncatedTail = len(data) > 0
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		var line Line
		if err := decode(bytes.TrimSuffix(data, []byte{'\n'}), &line); err != nil {
			return nil, err
		}
		if line.Key == "" || line.Cut.RunID == "" || line.Repeat < 1 {
			return nil, fmt.Errorf("invalid compaction cut line")
		}
		result.Lines = append(result.Lines, line)
	}
}

func begin(root string, h Header) (*writer, error) {
	dir := filepath.Join(root, "_compaction", h.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	if err := atomicJSON(filepath.Join(dir, "replay.json"), h); err != nil {
		return nil, err
	}
	rootDir, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	f, openErr := rootDir.OpenFile("cuts.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	err = errors.Join(openErr, rootDir.Close())
	if err != nil {
		return nil, err
	}
	w := &writer{dir: dir, file: f}
	if err := w.append(logHeader{Format: cutsFormat, Version: 1, Replay: h.ID}); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return w, nil
}

func atomicJSON(path string, value any) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	// Failed writes remain as private temporary evidence; the tool never deletes.
	if _, err := f.Write(append(data, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func validID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.Version() == 7 && u.String() == id
}

func (w *writer) append(value any) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	if _, err := w.file.Write(append(data, '\n')); err != nil {
		return err
	}
	return w.file.Sync()
}

func (w *writer) finish(h Header) error {
	return errors.Join(w.file.Close(), atomicJSON(filepath.Join(w.dir, "replay.json"), h))
}
