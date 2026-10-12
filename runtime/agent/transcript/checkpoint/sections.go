package checkpoint

import "strings"

// Parsed contains the section bodies selected by Sections, without boundary labels.
// Header includes its trailing newlines. No whitespace is trimmed.
type Parsed struct{ Header, Users, Summary, Receipt string }

// Sections splits checkpoint text. Callers must establish checkpoint identity
// from Message.Meta[transcript.CheckpointMetaKey], never from text alone.
// It selects the first user label, the last receipt label after it, and the last
// summary label between those boundaries. Missing boundaries return false.
// The split is exact for label-free Render components. Embedded section labels
// can make the original boundaries ambiguous; the same precedence still applies.
// In eval/evidence this affects only source attribution among user_index, summary
// and receipt, which is reported, never gated. Presence and gated measures are
// unaffected by that attribution.
func Sections(text string) (Parsed, bool) {
	header, rest, ok := strings.Cut(text, userLabel)
	if !ok {
		return Parsed{}, false
	}
	ri := strings.LastIndex(rest, receiptLabel)
	if ri < 0 {
		return Parsed{}, false
	}
	body := rest[:ri]
	si := strings.LastIndex(body, summaryLabel)
	if si < 0 {
		return Parsed{}, false
	}
	return Parsed{Header: header, Users: body[:si], Summary: body[si+len(summaryLabel):], Receipt: rest[ri+len(receiptLabel):]}, true
}
