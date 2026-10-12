package checkpoint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// UserMessage supplies record-authored content and its original provenance.
	UserMessage struct {
		ID      index.MessageID
		Origin  *index.Origin
		Message *model.Message
	}
	// UserLine identifies a record-authored user index line, never summary prose.
	UserLine struct {
		ID     index.MessageID
		Origin *index.Origin
		Text   string
		Bytes  int
	}
	// Limits caps pin count, index text bytes and held non-text parts.
	Limits struct {
		MaxPins, UserIndexLineBytes, MaxPinnedParts int
	}
	userInput struct {
		users      []UserMessage
		cited      []UserLine
		pinned     []index.MessageID
		budget     index.RenderBudget
		limits     Limits
		partTokens PartTokens
		currentPos int
		recordID   string
	}
	userRow struct {
		key   OrderKey
		text  string
		parts []model.Part
	}
	userSection struct {
		text  string
		parts []model.Part
	}
)

const compactUsersBytes = len("… [users rc-xxxxxxxx]\n")

// DefaultLimits holds the design's pin and index defaults.
var DefaultLimits = Limits{MaxPins: 32, UserIndexLineBytes: 160, MaxPinnedParts: 2}

// SelectPins selects the first user then a contiguous newest-first suffix.
func SelectPins(users []UserMessage, budget index.RenderBudget, limits Limits) []index.MessageID {
	currentPos := 1
	for _, user := range users {
		if user.Origin != nil {
			currentPos = max(currentPos, user.Origin.Pos+1)
		}
	}
	ordered := orderedUsers(users, currentPos)
	if len(ordered) == 0 || limits.MaxPins <= 0 {
		return nil
	}
	remaining := max(0, budget.KeepUserTokens)
	allowance := partAllowance{count: limits.MaxPinnedParts, tokens: max(0, budget.KeepUserTokens/4)}
	var pins []index.MessageID
	try := func(user UserMessage, first bool) bool {
		row, next, ok := pinRow(user, first, remaining, budget, currentPos, allowance)
		if !ok {
			return false
		}
		remaining -= len(row.text)
		allowance = next
		pins = append(pins, user.ID)
		return true
	}
	if !try(ordered[0], true) {
		return nil
	}
	for i := len(ordered) - 1; i > 0 && len(pins) < limits.MaxPins; i-- {
		if !try(ordered[i], false) {
			break
		}
	}
	return pins
}
func renderUsers(in userInput) userSection {
	ordered := orderedUsers(in.users, in.currentPos)
	if len(ordered) == 0 {
		return userSection{}
	}
	remaining := max(compactUsersBytes, in.budget.KeepUserTokens*BytesPerToken)
	rows, selected := pinnedRows(in, ordered)
	for _, row := range rows {
		remaining -= len(row.text)
	}
	// Demote pins only when they would consume the compact discovery reserve.
	for len(rows) < len(ordered) && remaining < compactUsersBytes && len(rows) > 0 {
		last := rows[len(rows)-1]
		rows = rows[:len(rows)-1]
		delete(selected, last.key)
		remaining += len(last.text)
	}
	rows = selectIndexRows(in, ordered, rows, selected, remaining)
	slices.SortStableFunc(rows, func(a, b userRow) int {
		return Compare(a.key, b.key)
	})
	var section userSection
	var text strings.Builder
	for _, row := range rows {
		text.WriteString(row.text)
		section.parts = append(section.parts, row.parts...)
	}
	section.text = text.String()
	return section
}

// orderedUsers copies before sorting: rendering never changes input order.
func orderedUsers(users []UserMessage, currentPos int) []UserMessage {
	ordered := make([]UserMessage, 0, len(users))
	for _, user := range users {
		if !onlyToolResults(user.Message) {
			ordered = append(ordered, user)
		}
	}
	slices.SortStableFunc(ordered, func(a, b UserMessage) int {
		return Compare(Key(a.ID, a.Origin, currentPos), Key(b.ID, b.Origin, currentPos))
	})
	return ordered
}

// onlyToolResults excludes result-only messages from the user index: the call's
// receipt covers their content. Mixed messages retain their user-owned parts.
func onlyToolResults(message *model.Message) bool {
	if message == nil || len(message.Parts) == 0 {
		return false
	}
	for _, part := range message.Parts {
		switch partValue(part).(type) {
		case model.ToolResultPart:
		default:
			return false
		}
	}
	return true
}

// pinnedRows rechecks the first stored pin, then newest pins until one cannot fit.
func pinnedRows(in userInput, ordered []UserMessage) ([]userRow, map[OrderKey]bool) {
	wanted := make(map[index.MessageID]bool, len(in.pinned))
	for _, id := range in.pinned {
		wanted[id] = true
	}
	var rows []userRow
	selected := map[OrderKey]bool{}
	remaining := max(0, in.budget.KeepUserTokens)
	allowance := partAllowance{count: in.limits.MaxPinnedParts, tokens: max(0, in.budget.KeepUserTokens/4), measure: in.partTokens}
	try := func(user UserMessage, first bool) bool {
		row, next, ok := pinRow(user, first, remaining, in.budget, in.currentPos, allowance)
		if !ok {
			return false
		}
		rows = append(rows, row)
		remaining -= len(row.text)
		allowance = next
		selected[row.key] = true
		return true
	}
	if in.limits.MaxPins <= 0 || !wanted[ordered[0].ID] || !try(ordered[0], true) {
		return rows, selected
	}
	for i := len(ordered) - 1; i > 0 && len(rows) < in.limits.MaxPins; i-- {
		if !wanted[ordered[i].ID] {
			continue
		}
		if !try(ordered[i], false) {
			break
		}
	}
	return rows, selected
}

// pinRow measures rendered text and labels rather than the entry measurement.
func pinRow(user UserMessage, first bool, remaining int, budget index.RenderBudget, currentPos int, allowance partAllowance) (userRow, partAllowance, bool) {
	label := Label(user.ID, user.Origin, currentPos)
	prefix := "[" + label + "] "
	text := pinText(user.Message, max(0, budget.KeepUserTokens*BytesPerToken/4))
	placeholders, parts := pinParts(user, label, &allowance)
	suffix := "\n"
	if placeholders != "" {
		suffix += placeholders + "\n"
	}
	available := remaining - len(prefix) - len(suffix)
	if available < 0 {
		return userRow{}, allowance, false
	}
	if first {
		text = HeadTail(text, min(available, max(0, budget.KeepUserTokens*BytesPerToken/4)))
	} else if len(text) > available {
		return userRow{}, allowance, false
	}
	row := userRow{key: Key(user.ID, user.Origin, currentPos), text: prefix + text + suffix, parts: parts}
	return row, allowance, true
}

// pinText folds text documents into bounded previews without changing other text.
func pinText(message *model.Message, documentBytes int) string {
	var texts []string
	for _, part := range message.Parts {
		switch value := partValue(part).(type) {
		case model.TextPart:
			texts = append(texts, value.Text)
		case model.CitationsPart:
			texts = append(texts, value.Text)
		case model.DocumentPart:
			if value.Text != "" || len(value.Chunks) > 0 {
				text := value.Text
				if len(value.Chunks) > 0 {
					text = strings.Join(value.Chunks, "\n")
				}
				texts = append(texts, HeadTail(text, documentBytes))
			}
		}
	}
	return strings.Join(texts, "\n")
}

// selectIndexRows chooses citations first, then newest uncited messages.
// The final chronological sort is independent of selection priority.
func selectIndexRows(in userInput, ordered []UserMessage, rows []userRow, selected map[OrderKey]bool, remaining int) []userRow {
	candidates := indexCandidates(in, ordered, selected)
	allRows := make([]userRow, 0, len(candidates))
	allBytes := 0
	for _, user := range candidates {
		row := indexRow(user, in.limits.UserIndexLineBytes, in.currentPos)
		allRows = append(allRows, row)
		allBytes += len(row.text)
	}
	if allBytes <= remaining {
		return append(rows, allRows...)
	}
	reserve := userCollapseReserve(in, ordered, selected)
	if reserve > remaining {
		reserve = compactUsersBytes
	}
	remaining -= reserve
	for _, row := range allRows {
		if len(row.text) > remaining {
			continue
		}
		rows = append(rows, row)
		selected[row.key] = true
		remaining -= len(row.text)
	}
	collapsed := collapsedUsers(in, ordered, selected)
	if collapsed.text != "" {
		if len(collapsed.text) > remaining+reserve {
			collapsed.text = compactHandle("users", in.recordID)
		}
		if len(collapsed.text) <= remaining+reserve {
			rows = append(rows, collapsed)
		}
	}
	return rows
}

// userCollapseReserve covers any endpoint left after priority selection.
// Count digits can only shrink; endpoint labels can grow when a cited first
// message or a newest message is selected, so reserve the longest label twice.
func userCollapseReserve(in userInput, ordered []UserMessage, selected map[OrderKey]bool) int {
	first, last, longest := "", "", 0
	for _, user := range ordered {
		if selected[Key(user.ID, user.Origin, in.currentPos)] {
			continue
		}
		label := Label(user.ID, user.Origin, in.currentPos)
		if first == "" {
			first = label
		}
		last = label
		longest = max(longest, len(label))
	}
	return len(collapsedUsers(in, ordered, selected).text) + longest*2 - len(first) - len(last)
}

// indexCandidates prioritizes cited records, then newest uncited records.
func indexCandidates(in userInput, ordered []UserMessage, selected map[OrderKey]bool) []UserMessage {
	cited := map[OrderKey]bool{}
	for _, line := range in.cited {
		cited[Key(line.ID, line.Origin, in.currentPos)] = true
	}
	candidates := make([]UserMessage, 0, len(ordered))
	for _, priority := range []bool{true, false} {
		for i := len(ordered) - 1; i >= 0; i-- {
			user := ordered[i]
			key := Key(user.ID, user.Origin, in.currentPos)
			if !selected[key] && cited[key] == priority {
				candidates = append(candidates, user)
			}
		}
	}
	return candidates
}

// indexRow names the original message and reports its text size when cut.
func indexRow(user UserMessage, lineBytes, currentPos int) userRow {
	text := userText(user.Message)
	line := runePrefix(firstLine(text), lineBytes)
	size := ""
	if len(line) < len(text) {
		size = fmt.Sprintf(" [%d bytes]", len(text))
	}
	label := Label(user.ID, user.Origin, currentPos)
	return userRow{key: Key(user.ID, user.Origin, currentPos), text: fmt.Sprintf("[%s, first line] %s%s\n", label, line, size)}
}

// collapsedUsers places the handle at the oldest omitted chronological key.
func collapsedUsers(in userInput, ordered []UserMessage, selected map[OrderKey]bool) userRow {
	var omitted []UserMessage
	for _, user := range ordered {
		if !selected[Key(user.ID, user.Origin, in.currentPos)] {
			omitted = append(omitted, user)
		}
	}
	if len(omitted) == 0 {
		return userRow{}
	}
	first, last := omitted[0], omitted[len(omitted)-1]
	start, end := Label(first.ID, first.Origin, in.currentPos), Label(last.ID, last.Origin, in.currentPos)
	return userRow{
		key:  Key(first.ID, first.Origin, in.currentPos),
		text: fmt.Sprintf("… %d user messages in %s–%s are not listed and may override earlier ones [users %s]\n", len(omitted), start, end, Handle("users", in.recordID)),
	}
}

// BudgetLimits resolves pin-count overrides from the recorded view budget.
func BudgetLimits(budget index.RenderBudget) Limits {
	limits := DefaultLimits
	if budget.MaxPins > 0 {
		limits.MaxPins = budget.MaxPins
	}
	if budget.MaxPinnedParts > 0 {
		limits.MaxPinnedParts = budget.MaxPinnedParts
	}
	return limits
}
