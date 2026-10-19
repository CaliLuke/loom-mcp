package statefulevaluation

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

type descriptor struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  jsontext.Value
	OutputSchema jsontext.Value
}
type discoveryKey struct{ Agent, Session string }
type retainedDescriptor struct {
	Descriptor descriptor
	Bytes      int
}
type discoverySnapshot struct {
	Generation string
	Entries    []retainedDescriptor
	Evictions  int
}
type discoveryState map[discoveryKey]*discoverySnapshot

const maxDiscoveredTools = 5
const maxDiscoveredBytes = 8192

var errDescriptorTooLarge = errors.New("descriptor exceeds retained schema byte cap")

// MarshalJSON keeps the external MCP spelling without converting schema numbers.
func (d descriptor) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"name": d.Name, "description": d.Description, "inputSchema": d.InputSchema, "outputSchema": d.OutputSchema}, json.Deterministic(true))
}

// UnmarshalJSON keeps raw schema bytes while selecting the four bounded fields.
func (d *descriptor) UnmarshalJSON(data []byte) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if err := json.Unmarshal(fields["name"], &d.Name); err != nil {
		return err
	}
	if err := json.Unmarshal(fields["description"], &d.Description); err != nil {
		return err
	}
	d.InputSchema = bytes.Clone(fields["inputSchema"])
	d.OutputSchema = bytes.Clone(fields["outputSchema"])
	return nil
}

func newDiscoveryState() discoveryState {
	return make(discoveryState)
}

// reveal retains an immutable descriptor once; oversized input leaves prior state intact.
func (s discoveryState) reveal(key discoveryKey, generation string, d descriptor) error {
	snapshot := s.current(key, generation)
	for _, entry := range snapshot.Entries {
		if entry.Descriptor.Name == d.Name {
			return nil
		}
	}
	encoded, err := json.Marshal(d, json.Deterministic(true))
	if err != nil {
		return err
	}
	if len(encoded) > maxDiscoveredBytes {
		return errDescriptorTooLarge
	}
	for len(snapshot.Entries) >= maxDiscoveredTools || s.bytes(key, generation)+len(encoded) > maxDiscoveredBytes {
		snapshot.Entries = snapshot.Entries[1:]
		snapshot.Evictions++
	}
	d.InputSchema = bytes.Clone(d.InputSchema)
	d.OutputSchema = bytes.Clone(d.OutputSchema)
	snapshot.Entries = append(snapshot.Entries, retainedDescriptor{Descriptor: d, Bytes: len(encoded)})
	return nil
}

func (s discoveryState) current(key discoveryKey, generation string) *discoverySnapshot {
	snapshot := s[key]
	if snapshot == nil || snapshot.Generation != generation {
		snapshot = &discoverySnapshot{Generation: generation}
		s[key] = snapshot
	}
	return snapshot
}

func (s discoveryState) names(key discoveryKey, generation string) []string {
	entries := s.current(key, generation).Entries
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Descriptor.Name)
	}
	return names
}

func (s discoveryState) bytes(key discoveryKey, generation string) int {
	total := 0
	for _, entry := range s.current(key, generation).Entries {
		total += entry.Bytes
	}
	return total
}
