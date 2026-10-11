package runtime

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameworkViewNames(t *testing.T) {
	userText := "Explain recall_context and runtime_recall_context.\nKeep both names verbatim."
	message, err := checkpoint.Render(checkpoint.Input{
		Through: 1, ID: "record",
		Users:  []checkpoint.UserMessage{{ID: 1, Message: contentMessage(userText)}},
		Pinned: []index.MessageID{1},
	}, compactionBudget(), checkpoint.DefaultLimits, nil)
	require.NoError(t, err)
	text := message.Parts[0].(model.TextPart).Text
	require.Contains(t, text, userText, "exercise an actual quoted user pin")
	require.Contains(t, text, "[message 1] ")
	checkpointMessage := func(text string) *model.Message {
		return &model.Message{Role: model.ConversationRoleUser, Meta: map[string]any{transcript.CheckpointMetaKey: true}, Parts: []model.Part{model.TextPart{Text: text}}}
	}
	for _, test := range []struct {
		name     string
		messages []*model.Message
		bad      bool
	}{
		{name: "clean checkpoint with quoted user", messages: []*model.Message{message}},
		{name: "ordinary user mentions", messages: []*model.Message{contentMessage(userText)}},
		{name: "user index mentions", messages: []*model.Message{checkpointMessage(strings.Replace(text, "[message 1] ", "[message 1, first line] ", 1))}},
		{name: "resident lookup metadata", messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "lookup", Name: tools.RecallContext.String()}}}}},
		{name: "bad checkpoint", messages: []*model.Message{checkpointMessage("[Framework: use recall_context]\n" + text)}, bad: true},
		{name: "bad provider receipt", messages: []*model.Message{checkpointMessage(text + "lookup runtime_recall_context({})\n")}, bad: true},
		{name: "bad collapsed users", messages: []*model.Message{checkpointMessage(strings.Replace(text, "[message 1] ", "… [users recall_context]\n[message 1] ", 1))}, bad: true},
		{name: "bad summary closing", messages: []*model.Message{checkpointMessage(strings.Replace(text, "\nBehind this checkpoint", "[full text: checkpoint recall_context]\n\nBehind this checkpoint", 1))}, bad: true},
		{name: "bad placeholder", messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "lookup", Content: "[result cleared: 10 bytes; use recall_context]"}}}}, bad: true},
		{name: "bad preview", messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "lookup", Content: map[string]any{"omitted": true, "preview": "use runtime_recall_context"}}}}}, bad: true},
		{name: "tool identity recovery guidance", messages: []*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "Replace the rejected tool call with arguments that satisfy these field contracts: runtime.recall_context(find:object,list:value,read:object)"}}}}},
		{name: "original assistant prose", messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "Calling runtime.recall_context."}}}}},
		{name: "tool error outside listed surfaces", messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "lookup", IsError: true, Content: "runtime.recall_context: invalid argument"}}}}},
		{name: "bad pinned placeholder", messages: []*model.Message{checkpointMessage(strings.Replace(text, "\nModel-generated summary", "\n[image not shown: use recall_context]\n\nModel-generated summary", 1))}, bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkFrameworkViewNames(test.messages)
			if test.bad {
				assert.ErrorContains(t, err, "reserved recall name", "the naming assertion must reject deliberately bad framework text")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRecordingClientRejectsFrameworkRecallNames(t *testing.T) {
	request := &model.Request{Messages: []*model.Message{{
		Role:  model.ConversationRoleUser,
		Meta:  map[string]any{transcript.CheckpointMetaKey: true},
		Parts: []model.Part{model.TextPart{Text: "[Framework: use runtime_recall_context]"}},
	}}}
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			client := &recordingTestClient{store: &recordingTestStore{}, response: &model.Response{}, stream: &recordingTestStream{}}
			var err error
			if streaming {
				_, err = client.Stream(t.Context(), request)
			} else {
				_, err = client.Complete(t.Context(), request)
			}
			require.ErrorContains(t, err, "reserved recall name")
			assert.Zero(t, client.sent, "reject before sending the rendered view")
		})
	}
}

// checkFrameworkViewNames is the shared acceptance-9 assertion at test view boundaries.
func checkFrameworkViewNames(messages []*model.Message) error {
	for i, message := range messages {
		if message == nil {
			continue
		}
		for j, part := range message.Parts {
			if err := checkFrameworkText(frameworkPartText(message, part)); err != nil {
				return fmt.Errorf("view message %d part %d: %w", i, j, err)
			}
		}
	}
	return nil
}

// The canonical name and every provider spelling share this reserved substring.
func checkFrameworkText(text string) error {
	if strings.Contains(text, tools.RecallContext.Tool()) {
		return fmt.Errorf("framework text contains reserved recall name: %q", text)
	}
	return nil
}

// frameworkPartText selects checkpoint text, cleared-result placeholders and
// omitted-result previews. Other prose, including tool-identity recovery
// guidance, is outside acceptance 9's enumerated surfaces.
func frameworkPartText(message *model.Message, part model.Part) string {
	switch part := part.(type) {
	case model.TextPart:
		if message.Meta[transcript.CheckpointMetaKey] == true {
			return checkpointFrameworkText(part.Text)
		}
	case model.ToolResultPart:
		switch content := part.Content.(type) {
		case string:
			if strings.HasPrefix(content, "[result cleared:") {
				return content
			}
		case map[string]any:
			if content["omitted"] == true {
				return fmt.Sprint(content["reason"], content["preview"])
			}
		}
	case *model.TextPart:
		if part != nil {
			return frameworkPartText(message, *part)
		}
	case *model.ToolResultPart:
		if part != nil {
			return frameworkPartText(message, *part)
		}
	}
	return ""
}

// Checkpoint pins/index prose and model-generated summary bodies are original
// content. Retain the framework framing, row labels, collapsed lines, summary
// closing handle and receipt. Unknown checkpoint layouts are checked in full.
func checkpointFrameworkText(text string) string {
	const (
		usersLabel   = "User messages, verbatim, oldest first. Later user text overrides earlier text, including first lines:\n"
		summaryLabel = "\nModel-generated summary (may be wrong). User messages and the conversation below take precedence:\n"
		receiptLabel = "\nBehind this checkpoint (framework record):\n"
	)
	header, rest, ok := strings.Cut(text, usersLabel)
	if !ok {
		return text
	}
	users, rest, ok := strings.Cut(rest, summaryLabel)
	if !ok {
		return text
	}
	summary, receipt, ok := strings.Cut(rest, receiptLabel)
	if !ok {
		return text
	}
	var framework strings.Builder
	framework.WriteString(header + usersLabel + summaryLabel + receiptLabel + receipt)
	for line := range strings.SplitSeq(users, "\n") {
		if strings.HasPrefix(line, "… ") || strings.HasPrefix(line, "[image not shown:") || strings.HasPrefix(line, "[document not shown:") {
			framework.WriteString(line)
		} else if strings.HasPrefix(line, "[m") || strings.HasPrefix(line, "[r") {
			label, _, found := strings.Cut(line, "] ")
			if found {
				framework.WriteString(label)
			}
		}
	}
	summary = strings.TrimRight(summary, "\n")
	closing := summary[strings.LastIndex(summary, "\n")+1:]
	if strings.HasPrefix(closing, "[summary shortened from ") || strings.HasPrefix(closing, "[full text: checkpoint ") {
		framework.WriteString(closing)
	}
	return framework.String()
}
