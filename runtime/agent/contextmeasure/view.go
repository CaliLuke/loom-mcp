package contextmeasure

import (
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ViewBearing matches canonical role/parts hashes against a view snapshot,
// excluding Meta and cache points. It does not implement call-record matching.
func ViewBearing(req *model.Request, view []*model.Message) (bool, error) {
	if req == nil {
		return false, errors.New("contextmeasure: nil request")
	}
	if len(view) == 0 || len(req.Messages) == 0 {
		return false, nil
	}
	hashes := make(map[[sha256.Size]byte]struct{}, len(view))
	for _, message := range view {
		if message == nil {
			continue
		}
		hash, err := messageHash(message)
		if err != nil {
			return false, err
		}
		hashes[hash] = struct{}{}
	}
	for _, message := range req.Messages {
		if message == nil {
			continue
		}
		hash, err := messageHash(message)
		if err != nil {
			return false, err
		}
		if _, match := hashes[hash]; match {
			return true, nil
		}
	}
	return false, nil
}

func messageHash(message *model.Message) ([sha256.Size]byte, error) {
	canonical := model.Message{Role: message.Role}
	for _, part := range message.Parts {
		normalized, err := normalizePart(part)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		if _, cache := normalized.(model.CacheCheckpointPart); !cache {
			canonical.Parts = append(canonical.Parts, normalized)
		}
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("contextmeasure: message hash: %w", err)
	}
	// Message.MarshalJSON owns an inner encoding. Normalize its raw object keys
	// too, while preserving exact integer/decimal tokens rather than using floats.
	value := jsontext.Value(data)
	if err := value.Canonicalize(jsontext.CanonicalizeRawInts(false), jsontext.CanonicalizeRawFloats(false)); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("contextmeasure: canonical message: %w", err)
	}
	return sha256.Sum256(value), nil
}
