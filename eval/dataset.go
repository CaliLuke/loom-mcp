package eval

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
)

// Dataset is an immutable collection with an eagerly captured, deferred error.
// Its zero value is an empty, invalid dataset.
type Dataset struct {
	items     []Item
	fromItems bool
	source    string
	err       error
}

// ErrEmptyDataset means a dataset has no items.
var ErrEmptyDataset = errors.New("eval: empty dataset")

// Items validates and owns copies of the supplied items.
func Items(items ...Item) Dataset {
	d := Dataset{source: "go", fromItems: true, items: make([]Item, len(items))}
	seen := make(map[string]bool, len(items))
	var problems []error
	for n, item := range items {
		d.items[n] = cloneItem(item)
		problems = append(problems, prefixProblems(fmt.Sprintf("item %q: ", item.ID), validateItem(item))...)
		if seen[item.ID] {
			problems = append(problems, fmt.Errorf("item %q: duplicate ID", item.ID))
		}
		seen[item.ID] = true
	}
	if len(items) == 0 {
		problems = append(problems, ErrEmptyDataset)
	}
	d.err = errors.Join(problems...)
	return d
}

// JSONL eagerly reads a file and defers read and decoding errors to Err.
func JSONL(fsys fs.FS, name string) Dataset {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Dataset{source: name, err: err}
	}
	d, err := DecodeJSONL(bytes.NewReader(data), name)
	d.err = err
	return d
}

// Items returns a deep copy of the dataset's items.
func (d Dataset) Items() []Item {
	items := make([]Item, len(d.items))
	for n, item := range d.items {
		items[n] = cloneItem(item)
	}
	return items
}

// Len returns the number of items.
func (d Dataset) Len() int {
	return len(d.items)
}

// Err returns the deferred validation or read error.
func (d Dataset) Err() error {
	if d.err != nil {
		return d.err
	}
	if len(d.items) == 0 {
		return ErrEmptyDataset
	}
	return nil
}

// Source returns "go" or the file name used at construction.
func (d Dataset) Source() string {
	return d.source
}

// Hash returns sha256:<lowercase hex> of canonical JSONL, or empty for invalid data.
func (d Dataset) Hash() string {
	var buf bytes.Buffer
	if err := EncodeJSONL(&buf, d); err != nil {
		return ""
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(buf.Bytes()))
}
