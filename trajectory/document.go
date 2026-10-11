package trajectory

import (
	"bytes"
	_ "embed"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
)

// Document wraps exactly one trajectory or session in a versioned JSON envelope.
type Document struct {
	Schema     string      `json:"schema"`
	Trajectory *Trajectory `json:"trajectory,omitzero"`
	Session    Session     `json:"session,omitzero"`
}

// SchemaVersion identifies the trajectory document's field and meaning contract.
const SchemaVersion = "loom.trajectory.v1"

//go:embed schema/trajectory.v1.schema.json
var documentSchema []byte

// EncodeDocument encodes one trajectory using the canonical callrecord codec.
func EncodeDocument(t *Trajectory) ([]byte, error) {
	if t == nil {
		return nil, errors.New("trajectory: document requires a trajectory")
	}
	return callrecord.Encode(Document{Schema: SchemaVersion, Trajectory: t})
}

// EncodeSessionDocument encodes a caller-ordered session using the canonical codec.
func EncodeSessionDocument(s Session) ([]byte, error) {
	if s == nil {
		return nil, errors.New("trajectory: document requires a session")
	}
	return callrecord.Encode(Document{Schema: SchemaVersion, Session: s})
}

// Schema returns the embedded JSON Schema 2020-12 for trajectory documents.
func Schema() []byte {
	return bytes.Clone(documentSchema)
}
