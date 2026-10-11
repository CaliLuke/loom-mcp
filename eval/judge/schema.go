package judge

import (
	"encoding/json/jsontext"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxSchemaBytes = 1024 * 1024
	claimSchema    = `{"type":"object","properties":{"label":{"type":"string","enum":["entailed","contradicted","not_addressed","indeterminate"]},"rationale":{"type":"string","minLength":1}},"required":["label","rationale"],"additionalProperties":false}`
)

var identifier = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func validateInput(in Input) error {
	if !identifier.MatchString(in.Subject) || !utf8.ValidString(in.Text) || !utf8.ValidString(in.Task) || !utf8.ValidString(in.Reference) || len(in.Claims) == 0 {
		return fmt.Errorf("%w: subject, UTF-8 text and at least one claim are required", ErrInvalidInput)
	}
	seen := make(map[string]bool, len(in.Claims))
	for _, claim := range in.Claims {
		if !identifier.MatchString(claim.ID) || seen[claim.ID] || claim.Text == "" || !utf8.ValidString(claim.Text) {
			return fmt.Errorf("%w: claim IDs must be unique identifiers and text must be non-empty UTF-8", ErrInvalidInput)
		}
		seen[claim.ID] = true
	}
	return nil
}

// buildSchema preserves claim order; validated identifiers need no JSON escaping.
func buildSchema(claims []Claim) (jsontext.Value, error) {
	var out strings.Builder
	out.WriteString(`{"type":"object","properties":{`)
	for i, claim := range claims {
		if i > 0 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, `"%s":%s`, claim.ID, claimSchema)
		if out.Len() > maxSchemaBytes {
			return nil, ErrSchemaTooLarge
		}
	}
	out.WriteString(`},"required":[`)
	for i, claim := range claims {
		if i > 0 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, `"%s"`, claim.ID)
	}
	out.WriteString(`],"additionalProperties":false}`)
	if out.Len() > maxSchemaBytes {
		return nil, ErrSchemaTooLarge
	}
	return jsontext.Value(out.String()), nil
}
