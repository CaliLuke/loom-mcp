package checkpoint

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"strings"
)

// CarryUsers returns exactly the selected text of pins and user index rows.
// Labels are regenerated from immutable origins in the next run. Non-text pin
// bodies are not copied; their rendered placeholders remain until multimodal carry.
func CarryUsers(in Input, budget index.RenderBudget) (pins, lines []UserLine) {
	rows := selectedUserRows(userInput{users: in.Users, cited: in.CitedUsers, pinned: in.Pinned, budget: budget, limits: BudgetLimits(budget), currentPos: in.CurrentPos, recordID: in.ID})
	for _, row := range rows {
		for _, user := range in.Users {
			if Key(user.ID, user.Origin, in.CurrentPos) != row.key {
				continue
			}
			_, text, found := strings.Cut(row.text, "] ")
			if !found {
				break
			}
			line := UserLine{ID: user.ID, Origin: user.Origin, Text: strings.TrimSuffix(text, "\n"), Bytes: len(text)}
			if strings.HasPrefix(row.text, "["+Label(user.ID, user.Origin, in.CurrentPos)+"] ") {
				pins = append(pins, line)
			} else {
				lines = append(lines, line)
			}
			break
		}
	}
	return pins, lines
}
