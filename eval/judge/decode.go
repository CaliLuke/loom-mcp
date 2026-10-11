package judge

import (
	"encoding/json/v2"
	"fmt"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type wire struct {
	Label     Label  `json:"label"`
	Rationale string `json:"rationale"`
}

func decode(response *model.Response, claims []Claim) ([]Judgment, error) {
	if response == nil || len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != toolName {
		return nil, fmt.Errorf("expected exactly one %s tool call", toolName)
	}
	var values map[string]wire
	if err := json.Unmarshal(response.ToolCalls[0].Payload, &values, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	expected := make(map[string]bool, len(claims))
	for _, claim := range claims {
		expected[claim.ID] = true
	}
	for id := range values {
		if !expected[id] {
			return nil, fmt.Errorf("unknown claim %q", id)
		}
	}
	judgments := make([]Judgment, 0, len(claims))
	for _, claim := range claims {
		value, ok := values[claim.ID]
		if !ok {
			return nil, fmt.Errorf("missing claim %q", claim.ID)
		}
		if !value.Label.Valid() {
			return nil, fmt.Errorf("claim %q has an invalid label", claim.ID)
		}
		if value.Rationale == "" || !utf8.ValidString(value.Rationale) {
			return nil, fmt.Errorf("claim %q requires a non-empty UTF-8 rationale", claim.ID)
		}
		judgments = append(judgments, Judgment{ClaimID: claim.ID, Label: value.Label, Rationale: value.Rationale})
	}
	return judgments, nil
}
