package candidate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
)

// read confines all store access, rejects symlinks and requires exactly one JSON value.
func (s *Store) read(name string, value any) (resultErr error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	info, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.Join(ErrNotFound, err)
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("candidate: store entry must be a regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("candidate: trailing JSON data")
	}
	return nil
}

// write atomically publishes one file inside the store. Frozen files are never
// replaced by an ordinary mutation; their bytes are verified before reuse.
func (s *Store) write(name string, value any) (resultErr error) {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("candidate: store entry must be a regular file")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	temp := ".pending-" + randomID()
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err := root.Remove(temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	_, writeErr := io.Copy(file, bytes.NewReader(data))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
