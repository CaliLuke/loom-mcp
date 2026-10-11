package eval

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

func syncDirectory(dir string) error {
	f, err := os.OpenInRoot(dir, ".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// appendFile writes each JSONL line in exactly one O_APPEND write and fsyncs it.
func appendFile(path string, data []byte, create bool) (err error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, root.Close())
	}()
	flags := os.O_WRONLY | os.O_APPEND
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, err := root.OpenFile(filepath.Base(path), flags, 0600)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	return errors.Join(writeErr, f.Sync(), f.Close())
}

// initializeStoreDir creates the gitignore without changing caller-owned permissions.
func initializeStoreDir(dir string) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, root.Close())
	}()
	f, err := root.OpenFile(".gitignore", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString("*\n")
	return errors.Join(writeErr, f.Sync(), f.Close())
}

// stageExperiment syncs both headers in a hidden, unpublished directory.
func (s *Store) stageExperiment(h ExperimentHeader) (string, error) {
	staging, err := os.MkdirTemp(s.dir, ".experiment.tmp-*")
	if err != nil {
		return "", err
	}
	if err := s.writeHeader(staging, h); err != nil {
		return "", err
	}
	data, err := encodeStore(attemptsHeader{Format: attemptsFormat, Version: 1, Experiment: h.ID})
	if err != nil {
		return "", err
	}
	if err := appendFile(filepath.Join(staging, "attempts.jsonl"), append(data, '\n'), true); err != nil {
		return "", err
	}
	if err := syncDirectory(staging); err != nil {
		return "", err
	}
	return staging, nil
}

// writeHeader commits only after the temporary file has been flushed and closed.
// Failed temp files are harmless: readers only open experiment.json.
func (s *Store) writeHeader(dir string, h ExperimentHeader) error {
	data, err := encodeStore(h)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".experiment.json.tmp-*")
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if s.beforeRename != nil {
		if err := s.beforeRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, "experiment.json")); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// publishDirectory exposes an experiment only after both staged headers are durable.
// Failed hidden staging directories stay invisible to store readers.
func (s *Store) publishDirectory(staging, dir string) error {
	if s.beforeRename != nil {
		if err := s.beforeRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, dir); err != nil {
		return err
	}
	return syncDirectory(s.dir)
}
