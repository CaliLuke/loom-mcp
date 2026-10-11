package eval

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
)

// DecodeError identifies the source and one-based line of a JSONL error.
type DecodeError struct {
	Source string
	Line   int
	Err    error
}

type jsonlHeader struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
}

const jsonlFormat = "loom-mcp.eval.items"
const maxLineBytes = 16 << 20

// DecodeJSONL reads strict version-1 item JSONL with a mandatory header.
// Raw data and argument number tokens retain their exact spelling.
func DecodeJSONL(r io.Reader, source string) (Dataset, error) {
	d := Dataset{source: source}
	fail := func(line int, err error) (Dataset, error) {
		d.err = &DecodeError{Source: source, Line: line, Err: err}
		return d, d.err
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), maxLineBytes+2)
	line := 0
	seen := make(map[string]bool)
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		if err := validateLine(data); err != nil {
			return fail(line, err)
		}
		if line == 1 {
			if err := decodeHeader(data); err != nil {
				return fail(line, err)
			}
			continue
		}
		item, err := decodeItem(data, seen)
		if err != nil {
			return fail(line, err)
		}
		seen[item.ID] = true
		d.items = append(d.items, item)
	}
	if err := scanner.Err(); err != nil {
		return fail(line+1, err)
	}
	if line == 0 {
		return fail(1, fmt.Errorf("missing header"))
	}
	if len(d.items) == 0 {
		return fail(line+1, ErrEmptyDataset)
	}
	return d, nil
}

// EncodeJSONL writes canonical version-1 JSONL with LF endings and a final LF.
func EncodeJSONL(w io.Writer, d Dataset) error {
	if err := d.Err(); err != nil {
		return err
	}
	if err := writeLine(w, jsonlHeader{Format: jsonlFormat, Version: 1}); err != nil {
		return err
	}
	for _, item := range d.items {
		if err := writeLine(w, item); err != nil {
			return err
		}
	}
	return nil
}

// Error reports the JSONL location and underlying failure.
func (e *DecodeError) Error() string {
	return fmt.Sprintf("eval: %s:%d: %v", e.Source, e.Line, e.Err)
}

// Unwrap exposes the underlying decoding or item validation error.
func (e *DecodeError) Unwrap() error {
	return e.Err
}

func validateLine(data []byte) error {
	if len(data) > maxLineBytes {
		return fmt.Errorf("line exceeds %d bytes", maxLineBytes)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("blank line")
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("line must be an object")
	}
	return nil
}

func decodeItem(data []byte, seen map[string]bool) (Item, error) {
	var item Item
	if err := json.Unmarshal(data, &item, json.RejectUnknownMembers(true)); err != nil {
		return Item{}, err
	}
	if err := validateItem(item); err != nil {
		return Item{}, err
	}
	if seen[item.ID] {
		return Item{}, fmt.Errorf("item %q: duplicate ID", item.ID)
	}
	return item, nil
}

func decodeHeader(data []byte) error {
	var header jsonlHeader
	if err := json.Unmarshal(data, &header, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if header.Format != jsonlFormat || header.Version != 1 {
		return fmt.Errorf("expected %s version 1 header", jsonlFormat)
	}
	return nil
}

func writeLine(w io.Writer, value any) error {
	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return err
	}
	canonical := jsontext.Value(data)
	if err := canonical.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	n, err := w.Write(canonical)
	if err != nil {
		return err
	}
	if n != len(canonical) {
		return io.ErrShortWrite
	}
	return nil
}
