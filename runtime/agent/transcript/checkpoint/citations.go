package checkpoint

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// Citation is an exact bracket reference; Pos zero means the summary's run.
	Citation struct {
		Pos        int
		ID         index.MessageID
		Start, End int
	}
	// CitationTarget distinguishes user-authored entries from user-role tool results.
	CitationTarget struct {
		Line UserLine
		Kind index.EntryKind
		Role model.ConversationRole
	}
	// CitationResolver resolves only authorized records and checks checkpoint coverage.
	CitationResolver interface {
		// Resolve returns canonical entry kind, role and record-authored user text.
		Resolve(OrderKey) (CitationTarget, bool, error)
		// Behind checks coverage in the referenced run, including predecessor runs.
		Behind(OrderKey) bool
	}
)

var (
	citationPattern = regexp.MustCompile(`\[(?:r([1-9][0-9]*) )?message ([1-9][0-9]*)\]`)
	// ErrCitesNonUser rejects citations to assistant messages and tool results.
	ErrCitesNonUser = errors.New("citation does not name a user message")
	// ErrCitesUnknown rejects citations to missing messages.
	ErrCitesUnknown = errors.New("citation names an unknown message")
	// ErrCitesNotBehind rejects citations outside checkpoint coverage.
	ErrCitesNotBehind = errors.New("citation is not behind the checkpoint")
)

// ParseCitations accepts exactly [message N] and [rK message N].
func ParseCitations(text string) []Citation {
	var citations []Citation
	for _, span := range citationPattern.FindAllStringSubmatchIndex(text, -1) {
		id, err := strconv.ParseUint(text[span[4]:span[5]], 10, 64)
		if err != nil {
			continue
		}
		pos := 0
		if span[2] >= 0 {
			pos, err = strconv.Atoi(text[span[2]:span[3]])
			if err != nil {
				continue
			}
		}
		citations = append(citations, Citation{Pos: pos, ID: index.MessageID(id), Start: span[0], End: span[1]})
	}
	return citations
}

// Qualify binds bare citations to the original summary run before copying.
func Qualify(text string, summaryPos, currentPos int) string {
	if summaryPos == currentPos {
		return text
	}
	var result strings.Builder
	offset := 0
	for _, citation := range ParseCitations(text) {
		result.WriteString(text[offset:citation.Start])
		if citation.Pos == 0 {
			fmt.Fprintf(&result, "[r%d message %d]", summaryPos, citation.ID)
		} else {
			result.WriteString(text[citation.Start:citation.End])
		}
		offset = citation.End
	}
	result.WriteString(text[offset:])
	return result.String()
}

// ValidateCitations returns unique record-authored user lines in citation order.
func ValidateCitations(text string, summaryPos int, resolver CitationResolver) ([]UserLine, error) {
	var lines []UserLine
	seen := map[OrderKey]bool{}
	for _, citation := range ParseCitations(text) {
		pos := citation.Pos
		if pos == 0 {
			pos = summaryPos
		}
		key := OrderKey{Pos: pos, ID: citation.ID}
		if seen[key] {
			continue
		}
		target, found, err := resolver.Resolve(key)
		if err != nil {
			return nil, fmt.Errorf("resolve citation r%d message %d: %w", pos, citation.ID, err)
		}
		if !found {
			return nil, fmt.Errorf("r%d message %d: %w", pos, citation.ID, ErrCitesUnknown)
		}
		if !resolver.Behind(key) {
			return nil, fmt.Errorf("r%d message %d: %w", pos, citation.ID, ErrCitesNotBehind)
		}
		if target.Role != model.ConversationRoleUser || (target.Kind != index.Message && target.Kind != index.Clarification) {
			return nil, fmt.Errorf("r%d message %d: %w", pos, citation.ID, ErrCitesNonUser)
		}
		lines = append(lines, target.Line)
		seen[key] = true
	}
	return lines, nil
}
