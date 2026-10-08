package model

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// DecodeSchemaJSON preserves numeric tokens for exact comparisons while
// retaining JSON v2's strict UTF-8 and unique object member requirements.
func DecodeSchemaJSON(raw []byte) (any, error) {
	if !jsontext.Value(raw).IsValid() {
		return nil, errors.New("invalid JSON")
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(raw))
}
