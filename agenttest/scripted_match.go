package agenttest

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Match checks whether a request can consume a turn; nil means it matches.
type Match func(*model.Request) error

// LastUserContains matches text in the last user message, ignoring older messages.
func LastUserContains(text string) Match {
	return func(request *model.Request) error {
		for index := len(request.Messages) - 1; index >= 0; index-- {
			message := request.Messages[index]
			if message != nil && message.Role == model.ConversationRoleUser {
				if strings.Contains(messageText(message), text) {
					return nil
				}
				break
			}
		}
		return fmt.Errorf("last user message does not contain %q", text)
	}
}

// HasTool matches requests advertising the named tool.
func HasTool(tool tools.Named) Match {
	name := tool.Ident()
	return func(request *model.Request) error {
		if requestHasTool(request, name) {
			return nil
		}
		return fmt.Errorf("request lacks tool %q", name)
	}
}

// LacksTool matches requests that do not advertise the named tool.
func LacksTool(tool tools.Named) Match {
	name := tool.Ident()
	return func(request *model.Request) error {
		if !requestHasTool(request, name) {
			return nil
		}
		return fmt.Errorf("request unexpectedly has tool %q", name)
	}
}

// LastToolResult matches the latest result of the named tool by tracing its
// ToolUseID to a preceding tool use. String results are plain text; other values
// are JSON encoded before checking the substring.
func LastToolResult(tool tools.Named, contains string) Match {
	name := tool.Ident()
	return func(request *model.Request) error {
		result := lastToolResult(request, name)
		if result == nil {
			return fmt.Errorf("no result for tool %q", name)
		}
		text, ok := result.Content.(string)
		if !ok {
			encoded, err := json.Marshal(result.Content)
			if err != nil {
				return fmt.Errorf("encode result for tool %q: %w", name, err)
			}
			text = string(encoded)
		}
		if !strings.Contains(text, contains) {
			return fmt.Errorf("last result for tool %q does not contain %q", name, contains)
		}
		return nil
	}
}

// LastToolResultParts matches the latest named result's attachments in order.
func LastToolResultParts(tool tools.Named, parts ...model.ToolResultContentPart) Match {
	name := tool.Ident()
	return func(request *model.Request) error {
		result := lastToolResult(request, name)
		if result == nil {
			return fmt.Errorf("no result for tool %q", name)
		}
		if !reflect.DeepEqual(result.Parts, parts) {
			return fmt.Errorf("last result attachments for tool %q differ", name)
		}
		return nil
	}
}

// ModelClass matches the logical model class requested by the caller.
func ModelClass(class model.ModelClass) Match {
	return func(request *model.Request) error {
		if request.ModelClass != class {
			return fmt.Errorf("model class %q does not match %q", request.ModelClass, class)
		}
		return nil
	}
}

func matchRequest(matches []Match, request *model.Request) error {
	for _, match := range matches {
		if match == nil {
			return errors.New("nil request matcher")
		}
		if err := match(request); err != nil {
			return err
		}
	}
	return nil
}

func requestHasTool(request *model.Request, name tools.Ident) bool {
	for _, tool := range request.Tools {
		if tool != nil && tool.Name == string(name) {
			return true
		}
	}
	return false
}

func requestSummary(request *model.Request) string {
	identity := request.Model
	if identity == "" {
		identity = string(request.ModelClass)
	}
	var role model.ConversationRole
	var text string
	if len(request.Messages) > 0 {
		last := request.Messages[len(request.Messages)-1]
		if last != nil {
			role, text = last.Role, messageText(last)
		}
	}
	runes := []rune(text)
	if len(runes) > 160 {
		text = string(runes[:160]) + "…"
	}
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if tool != nil {
			names = append(names, tool.Name)
		}
	}
	return fmt.Sprintf("model=%q last_role=%q last_text=%q tools=[%s]", identity, role, text, strings.Join(names, ", "))
}

func messageText(message *model.Message) string {
	var text strings.Builder
	for _, part := range message.Parts {
		switch value := part.(type) {
		case model.TextPart:
			text.WriteString(value.Text)
		case *model.TextPart:
			if value != nil {
				text.WriteString(value.Text)
			}
		}
	}
	return text.String()
}

// lastToolResult correlates results with the preceding canonical tool use.
func lastToolResult(request *model.Request, name tools.Ident) *model.ToolResultPart {
	calls := make(map[string]string)
	var result *model.ToolResultPart
	for _, message := range request.Messages {
		if message == nil {
			continue
		}
		for _, part := range message.Parts {
			switch value := part.(type) {
			case model.ToolUsePart:
				calls[value.ID] = value.Name
			case *model.ToolUsePart:
				if value != nil {
					calls[value.ID] = value.Name
				}
			case model.ToolResultPart:
				if calls[value.ToolUseID] == string(name) {
					result = &value
				}
			case *model.ToolResultPart:
				if value != nil && calls[value.ToolUseID] == string(name) {
					result = value
				}
			}
		}
	}

	return result
}
