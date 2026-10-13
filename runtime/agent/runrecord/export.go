package runrecord

import (
	jsonopts "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// Export is the version-1 self-contained run-tree encoding. It includes rendered
// transcripts so reading it never depends on historical template implementations.
type Export struct {
	// Version identifies this export schema.
	Version int `json:"version"`
	// RootRunID is the original content scope.
	RootRunID string `json:"root_run_id"`
	// Runs includes all evidence and rebuilt requests in parent-first order.
	Runs []Run `json:"runs"`
	// Content holds exact addressed bytes, including server-only content.
	Content map[runstore.Hash][]byte `json:"content"`
}

// FromExport decodes an export and verifies new attachment evidence entirely
// from its included content and canonical records.
func FromExport(data []byte) (*RunRecord, error) {
	var exported Export
	if err := json.Unmarshal(data, &exported, jsonopts.FormatDurationAsNano(true), json.RejectUnknownMembers(true), json.WithUnmarshalers(json.JoinUnmarshalers(json.UnmarshalFunc(decodeExportEvent), json.UnmarshalFunc(decodeExportSettings)))); err != nil {
		return nil, fmt.Errorf("decode runrecord export: %w", err)
	}
	if exported.Version != 1 || exported.RootRunID == "" {
		return nil, fmt.Errorf("runrecord: unsupported export version or missing root scope")
	}
	// Restore requests through the exact evidence decoder rather than the default
	// float64 decoding of dynamic Meta/Input/Content values.
	var raw struct {
		Runs []struct {
			Calls []struct {
				Request jsontext.Value `json:"request"`
			} `json:"calls"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &raw, jsonopts.FormatDurationAsNano(true)); err != nil {
		return nil, fmt.Errorf("decode exported requests: %w", err)
	}
	for i := range exported.Runs {
		for j := range exported.Runs[i].Calls {
			request, err := callrecord.DecodeRequest(raw.Runs[i].Calls[j].Request)
			if err != nil {
				return nil, err
			}
			call := &exported.Runs[i].Calls[j]
			call.Request = request
			actual, err := callrecord.RequestDigest(request)
			if err != nil {
				return nil, err
			}
			if actual != call.Started.Request.Request {
				return nil, fmt.Errorf("verify exported call: %w", callrecord.ErrRequestMismatch)
			}
		}
	}
	record := &RunRecord{RootRunID: exported.RootRunID, Runs: exported.Runs, Content: exported.Content}
	if err := verifyExportCompatibility(record); err != nil {
		return nil, err
	}
	return record, nil
}

// Export returns a versioned snapshot suitable for deterministic encoding.
func (r *RunRecord) Export() Export {
	return Export{Version: 1, RootRunID: r.RootRunID, Runs: r.Runs, Content: r.Content}
}

// Marshal returns deterministic JSON v2, including nanosecond durations.
func (e Export) Marshal() ([]byte, error) {
	data, err := json.Marshal(e, json.Deterministic(true), json.FormatNilSliceAsNull(true), jsonopts.FormatDurationAsNano(true), json.WithMarshalers(json.MarshalFunc(encodeExportEvent)))
	if err != nil {
		return nil, fmt.Errorf("encode runrecord export: %w", err)
	}
	ordered := jsontext.Value(data)
	if err := ordered.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return nil, fmt.Errorf("order runrecord export: %w", err)
	}
	return ordered, nil
}

// Event payloads are opaque bytes in the canonical log. Base64 on the export
// boundary preserves their original member ordering and whitespace exactly.
func encodeExportEvent(event *runlog.Event) ([]byte, error) {
	type wireEvent runlog.Event
	return json.Marshal(struct {
		*wireEvent
		Payload []byte
	}{wireEvent: (*wireEvent)(event), Payload: event.Payload}, json.Deterministic(true))
}

func decodeExportEvent(data []byte, event **runlog.Event) error {
	type wireEvent runlog.Event
	value := struct {
		*wireEvent
		Payload []byte
	}{wireEvent: new(wireEvent)}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	value.wireEvent.Payload = rawjson.Message(value.Payload)
	*event = (*runlog.Event)(value.wireEvent)
	return nil
}
