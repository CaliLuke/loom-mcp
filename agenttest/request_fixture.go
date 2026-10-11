package agenttest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type requestFixture struct {
	Version int            `json:"version"`
	ModelID string         `json:"model_id"`
	Source  *Source        `json:"source"`
	Request jsontext.Value `json:"request"`
	Digest  runstore.Hash  `json:"digest"`
}

// MarshalRequest encodes a deterministic version-1 fixture holding full prompts.
// Private completion validators and portable provider thinking signatures are
// not carried over; do not replay thinking signatures across providers.
func MarshalRequest(r Request) ([]byte, error) {
	req, err := r.Request()
	if err != nil {
		return nil, err
	}
	data, err := callrecord.Encode(req)
	if err != nil {
		return nil, err
	}
	digest, err := callrecord.RequestDigest(req)
	if err != nil {
		return nil, err
	}
	return callrecord.Encode(requestFixture{Version: 1, ModelID: r.modelID, Source: r.Source(), Request: data, Digest: digest})
}

// UnmarshalRequest verifies a version-1 fixture and rejects unknown members.
// Fixtures contain full prompts and omit private completion validators.
// Provider thinking signatures must not be replayed across providers.
func UnmarshalRequest(data []byte) (Request, error) {
	var fixture requestFixture
	if err := json.Unmarshal(data, &fixture, json.RejectUnknownMembers(true)); err != nil {
		return Request{}, fmt.Errorf("agenttest: decode request fixture: %w", err)
	}
	if fixture.Version != 1 {
		return Request{}, fmt.Errorf("agenttest: request fixture version %d is unsupported", fixture.Version)
	}
	req, err := callrecord.DecodeRequest(fixture.Request)
	if err != nil {
		return Request{}, err
	}
	if req == nil {
		return Request{}, fmt.Errorf("agenttest: fixture request is nil")
	}
	digest, err := callrecord.RequestDigest(req)
	if err != nil {
		return Request{}, err
	}
	if digest != fixture.Digest {
		return Request{}, fmt.Errorf("agenttest: request fixture digest: %w", callrecord.ErrRequestMismatch)
	}
	r, err := NewRequest(fixture.ModelID, req)
	r.source = fixture.Source
	return r, err
}

// LoadRequest loads a full-prompt fixture and fails t on read or verification errors.
// Private completion validators are absent; thinking signatures are not portable.
func LoadRequest(t testing.TB, path string) Request {
	t.Helper()
	data, err := readRequestFixture(path)
	if err != nil {
		t.Fatal(err)
		return Request{}
	}
	r, err := UnmarshalRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// SaveRequest writes a full-prompt fixture, creating new files with mode 0600.
// Private completion validators are absent; thinking signatures are not portable.
func SaveRequest(path string, r Request) error {
	data, err := MarshalRequest(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// readRequestFixture confines the file access to the explicitly selected folder.
func readRequestFixture(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	data, readErr := root.ReadFile(filepath.Base(path))
	return data, errors.Join(readErr, root.Close())
}
