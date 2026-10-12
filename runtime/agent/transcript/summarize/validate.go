package summarize

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type cutResolver struct {
	source checkpoint.CitationResolver
	pos    int
	to     index.MessageID
}

var (
	loosePattern   = regexp.MustCompile(`(?i)\[(?:r[0-9]+ )?message [0-9]+\]|\bmessages\s+[0-9]+\s*[-–—]\s*[0-9]+`)
	headingPattern = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+(.+?)\s*$`)
)

// Validate checks first-failure order before a checkpoint can be stored. Size
// estimation and citation-resolution infrastructure errors are returned intact.
// Earlier-run citations delegate to the recorded continuation resolver.
func Validate(in ValidationInput) (Validation, error) {
	result := Validation{LooseRefs: countLoose(in.Text)}
	if err := validateText(in); err != nil {
		return result, err
	}
	if in.RenderedCheckpoint == nil {
		return result, fmt.Errorf("summarize: missing rendered checkpoint estimate")
	}
	size, err := in.RenderedCheckpoint(in.Text)
	if err != nil {
		return result, err
	}
	if size >= in.ReplacedTokens {
		return result, &Rejection{Kind: "not_smaller"}
	}
	result.CitedUsers, err = checkpoint.ValidateCitations(in.Text, in.CurrentPos, cutResolver{in.Resolver, in.CurrentPos, in.To})
	if err != nil {
		return result, citationRejection(err)
	}
	if in.DefaultPrompt && !hasHeadings(in.Text) {
		return result, &Rejection{Kind: "missing_headings"}
	}
	if namesInternalTool(in.Text, in.RecallNames) {
		return result, &Rejection{Kind: "names_internal_tool"}
	}
	return result, nil
}

// validateText preserves the first three failures before any integration reads.
func validateText(in ValidationInput) error {
	var kind string
	switch {
	case strings.TrimSpace(in.Text) == "":
		kind = "empty"
	case in.Unfinished || in.StopReason == "length" || in.StopReason == "max_tokens":
		kind = "truncated"
	case checkpoint.BoundSummary(in.Text, in.SummaryBytes, "") != in.Text:
		kind = "too_large"
	}
	if kind != "" {
		return &Rejection{Kind: kind}
	}
	return nil
}

func citationRejection(err error) error {
	for _, failure := range []struct {
		err  error
		kind string
	}{
		{checkpoint.ErrCitesUnknown, "cites_unknown"},
		{checkpoint.ErrCitesNotBehind, "cites_not_behind"},
		{checkpoint.ErrCitesNonUser, "cites_non_user"},
	} {
		if errors.Is(err, failure.err) {
			return &Rejection{Kind: failure.kind}
		}
	}
	return err
}

func namesInternalTool(text string, names []string) bool {
	if strings.Contains(text, "recall_context") {
		return true
	}
	for _, name := range names {
		if name != "" && strings.Contains(text, name) {
			return true
		}
	}
	return false
}

func countLoose(text string) int {
	exact := map[int]int{}
	for _, citation := range checkpoint.ParseCitations(text) {
		exact[citation.Start] = citation.End
	}
	count := 0
	for _, span := range loosePattern.FindAllStringIndex(text, -1) {
		if exact[span[0]] != span[1] {
			count++
		}
	}
	return count
}

func hasHeadings(text string) bool {
	required := []string{"goal", "decisions", "facts", "progress", "open questions", "next"}
	found := map[string]bool{}
	fence := ""
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		match := headingPattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(match[1]))
		if end := strings.LastIndexAny(name, " \t"); end >= 0 && strings.Trim(name[end+1:], "#") == "" {
			name = strings.TrimSpace(name[:end])
		}
		if slices.Contains(required, name) {
			found[name] = true
		}
	}
	return len(found) == len(required)
}

func (r cutResolver) Resolve(key checkpoint.OrderKey) (checkpoint.CitationTarget, bool, error) {
	if key.Pos != r.pos && (key.Pos <= 0 || key.Pos >= r.pos) {
		return checkpoint.CitationTarget{}, false, nil
	}
	if r.source == nil {
		return checkpoint.CitationTarget{}, false, fmt.Errorf("summarize: missing citation resolver")
	}
	return r.source.Resolve(key)
}

func (r cutResolver) Behind(key checkpoint.OrderKey) bool {
	return r.source != nil && (key.Pos != r.pos || key.ID < r.to) && r.source.Behind(key)
}
