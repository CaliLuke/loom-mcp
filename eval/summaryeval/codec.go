package summaryeval

import (
	jsonopts "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func encode(value any) ([]byte, error) {
	data, err := json.Marshal(value, json.Deterministic(true), jsonopts.FormatDurationAsNano(true))
	if err != nil {
		return nil, err
	}
	raw := jsontext.Value(data)
	if err := raw.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return nil, err
	}
	return raw, nil
}

func decode(data []byte, value any) error {
	return json.Unmarshal(data, value, jsonopts.FormatDurationAsNano(true), json.RejectUnknownMembers(true))
}
