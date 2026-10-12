package continuation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// VisibleHash hashes only the canonical role and parts, ignoring message metadata.
func VisibleHash(message *model.Message) (runstore.Hash, error) {
	canonical := &model.Message{Role: message.Role}
	for _, part := range message.Parts {
		switch part.(type) {
		case model.CacheCheckpointPart, *model.CacheCheckpointPart:
		default:
			canonical.Parts = append(canonical.Parts, part)
		}
	}
	data, err := json.Marshal(canonical, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	value := jsontext.Value(data)
	if err := value.Canonicalize(jsontext.CanonicalizeRawInts(false), jsontext.CanonicalizeRawFloats(false)); err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return runstore.Hash("sha256:" + hex.EncodeToString(digest[:])), nil
}

// Match reports whether the caller supplies the entire previously visible prefix.
func Match(visible, incoming []runstore.Hash) bool {
	return len(incoming) >= len(visible) && slices.Equal(visible, incoming[:len(visible)])
}
