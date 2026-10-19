package inspect

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

const blobDirectory = "blobs"

// Open opens a host-owned workspace without changing original evidence stores.
func Open(dir string, experiments *eval.Store, candidates *candidate.Store, limits Limits) (*Workspace, error) {
	if experiments == nil || candidates == nil || limits.MaxArtifactBytes <= 0 || limits.MaxEntries <= 0 || limits.MaxPageEntries <= 0 || limits.MaxReadBytes <= 0 || int64(limits.MaxReadBytes) > limits.MaxArtifactBytes || limits.MaxPageEntries > limits.MaxEntries {
		return nil, ErrInvalidInput
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, ErrInvalidInput
	}
	for _, p := range []string{absolute, filepath.Join(absolute, "manifests"), filepath.Join(absolute, blobDirectory)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			return nil, ErrInvalidInput
		}
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrInvalidInput
		}
	}
	return &Workspace{dir: absolute, experiments: experiments, candidates: candidates, limits: limits}, nil
}

func validName(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\\x00")
}

func validOrigin(o Origin) bool {
	return o.ExperimentID != "" && o.Key.Scenario != "" && o.Key.Item != "" && o.Key.Variant != "" && o.Key.Repeat > 0
}

func validRole(r Role) bool { return r == Editor || r == Selector || r == Solver || r == Evaluator }

// readBoundedBody reads at most limit bytes plus one growth-detection byte.
func readBoundedBody(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return nil, ErrIntegrity
	}
	var probe [1]byte
	n, err := io.ReadFull(reader, probe[:])
	if n != 0 || !errors.Is(err, io.EOF) {
		return nil, ErrIntegrity
	}
	return data, nil
}

// readBlob repeats the size check on the opened file and bounds subsequent growth.
func readBlob(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > limit {
		closeErr := file.Close()
		return nil, errors.Join(ErrIntegrity, closeErr)
	}
	data, readErr := readBoundedBody(file, limit)
	if errors.Join(readErr, file.Close()) != nil {
		return nil, ErrIntegrity
	}
	return data, nil
}

func (w *Workspace) readFile(group, name string) ([]byte, error) {
	if (group != "manifests" && group != blobDirectory) || !validDigest(strings.TrimSuffix(name, ".json")) {
		return nil, ErrIntegrity
	}
	dir, err := os.Lstat(filepath.Join(w.dir, group))
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return nil, ErrIntegrity
	}
	info, err := os.Lstat(filepath.Join(w.dir, group, name))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || group == blobDirectory && info.Size() > w.limits.MaxArtifactBytes {
		return nil, ErrIntegrity
	}
	root, err := os.OpenRoot(w.dir)
	if err != nil {
		return nil, ErrIntegrity
	}
	var data []byte
	var readErr error
	if group == blobDirectory {
		data, readErr = readBlob(root, filepath.Join(group, name), w.limits.MaxArtifactBytes)
	} else {
		data, readErr = root.ReadFile(filepath.Join(group, name))
	}
	if errors.Join(readErr, root.Close()) != nil {
		return nil, ErrIntegrity
	}
	return data, nil
}

func (w *Workspace) load(ref ManifestRef) (diskManifest, error) {
	if !validDigest(string(ref)) {
		return diskManifest{}, ErrNotFound
	}
	data, err := w.readFile("manifests", string(ref)+".json")
	if errors.Is(err, os.ErrNotExist) {
		return diskManifest{}, ErrNotFound
	}
	if err != nil {
		return diskManifest{}, ErrIntegrity
	}
	var m diskManifest
	if strictDecode(data, &m) != nil || validateManifest(m, ref) != nil || len(m.Semantic.Entries) > w.limits.MaxEntries {
		return diskManifest{}, ErrIntegrity
	}
	return m, nil
}

// publishFile publishes immutable bytes and fsyncs the directory before returning.
func (w *Workspace) publishFile(group, name string, data []byte) (err error) {
	old, readErr := w.readFile(group, name)
	if readErr == nil {
		if string(old) != string(data) {
			return ErrIntegrity
		}
		return nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return ErrIntegrity
	}
	dir := filepath.Join(w.dir, group)
	file, err := os.CreateTemp(dir, ".publish-")
	if err != nil {
		return ErrIntegrity
	}
	defer func() {
		removeErr := os.Remove(file.Name())
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, ErrIntegrity)
		}
	}()
	_, writeErr := file.Write(data)
	if errors.Join(writeErr, file.Sync(), file.Close()) != nil {
		return ErrIntegrity
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, name)); err != nil {
		return ErrIntegrity
	}
	root, err := os.OpenRoot(w.dir)
	if err != nil {
		return ErrIntegrity
	}
	d, err := root.Open(group)
	closeErr := root.Close()
	if errors.Join(err, closeErr) != nil {
		return ErrIntegrity
	}
	if errors.Join(d.Sync(), d.Close()) != nil {
		return ErrIntegrity
	}
	return nil
}

func (w *Workspace) readProvider(ctx context.Context, m semanticManifest, s entrySpec, e Entry) ([]byte, error) {
	if e.Kind == ComponentDiff {
		return w.readFile(blobDirectory, e.Digest)
	}
	switch s.Provider.Kind {
	case blobProvider:
		return w.readFile(blobDirectory, s.Provider.Digest)
	case "candidate":
		f, err := w.candidates.Load(ctx, s.Provider.Candidate)
		if err != nil {
			return nil, err
		}
		return f.Read(s.Provider.Name)
	case "artifact":
		return w.experiments.ReadArtifact(ctx, m.Origin.ExperimentID, m.Origin.Key, s.Provider.Name)
	default:
		return nil, ErrIntegrity
	}
}
