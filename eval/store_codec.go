package eval

import (
	jsonopts "encoding/json"
	"encoding/json/v2"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
)

// Wire mirrors keep persistence names independent of existing public Go types.
type wireCheck struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	Diagnostic string `json:"diagnostic"`
}
type wireScore struct {
	Name      string   `json:"name"`
	Value     float64  `json:"value"`
	Threshold *float64 `json:"threshold,omitempty"`
}
type wireSelection struct {
	Variants  []string `json:"variants"`
	Scenarios []string `json:"scenarios"`
	Tags      []string `json:"tags"`
	Items     []string `json:"items"`
}
type wireScenario struct {
	ID            string        `json:"id"`
	Agent         agent.Ident   `json:"agent"`
	Tags          []string      `json:"tags"`
	Timeout       time.Duration `json:"timeout"`
	DatasetSource string        `json:"dataset_source"`
	DatasetHash   string        `json:"dataset_hash"`
	Items         []string      `json:"items"`
}
type wirePrompt struct {
	ID      prompt.Ident `json:"id"`
	Version string       `json:"version"`
}

var storeMarshalers = json.JoinMarshalers(
	json.MarshalFunc(func(v expect.Check) ([]byte, error) {
		return callrecord.Encode(wireCheck(v))
	}),
	json.MarshalFunc(func(v expect.Score) ([]byte, error) {
		return callrecord.Encode(wireScore(v))
	}),
	json.MarshalFunc(func(v Claim) ([]byte, error) {
		return callrecord.Encode(wireClaim(v))
	}),
	json.MarshalFunc(func(v Selection) ([]byte, error) {
		return callrecord.Encode(wireSelection(v))
	}),
	json.MarshalFunc(func(v ScenarioInfo) ([]byte, error) {
		return callrecord.Encode(wireScenario(v))
	}),
	json.MarshalFunc(func(v prompt.PromptRef) ([]byte, error) {
		return callrecord.Encode(wirePrompt(v))
	}),
)
var storeUnmarshalers = json.JoinUnmarshalers(
	json.UnmarshalFunc(func(b []byte, v *expect.Check) error {
		var w wireCheck
		err := json.Unmarshal(b, &w, json.RejectUnknownMembers(true), jsonopts.FormatDurationAsNano(true))
		*v = expect.Check(w)
		return err
	}),
	json.UnmarshalFunc(func(b []byte, v *expect.Score) error {
		var w wireScore
		err := json.Unmarshal(b, &w, json.RejectUnknownMembers(true), jsonopts.FormatDurationAsNano(true))
		*v = expect.Score(w)
		return err
	}),
	json.UnmarshalFunc(func(b []byte, v *Selection) error {
		var w wireSelection
		err := json.Unmarshal(b, &w, json.RejectUnknownMembers(true), jsonopts.FormatDurationAsNano(true))
		*v = Selection(w)
		return err
	}),
	json.UnmarshalFunc(func(b []byte, v *ScenarioInfo) error {
		var w wireScenario
		err := json.Unmarshal(b, &w, json.RejectUnknownMembers(true), jsonopts.FormatDurationAsNano(true))
		*v = ScenarioInfo(w)
		return err
	}),
	json.UnmarshalFunc(func(b []byte, v *prompt.PromptRef) error {
		var w wirePrompt
		err := json.Unmarshal(b, &w, json.RejectUnknownMembers(true), jsonopts.FormatDurationAsNano(true))
		*v = prompt.PromptRef(w)
		return err
	}),
	json.UnmarshalFunc(decodeStoredMessage),
)

// wireClaim maps the current Claim fields without coupling the two struct shapes.
// Subject and Tool retain the resolved criterion and selector.
func wireClaim(v Claim) StoredClaim {
	return StoredClaim{ID: v.ID, Text: v.Text, Informational: v.Informational, Subject: string(v.Subject), Tool: v.Tool}
}
