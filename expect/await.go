package expect

import (
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// Await checks any observed prompt of the requested planner await kind without
// consuming the tool-call cursor. Unverified prompts fail before decoding or
// invoking predicates. It is not applicable to Calls.
func Await(kind planner.AwaitItemKind, predicate func(planner.AwaitItem) error) Expectation {
	return runExpectation{name: "await " + string(kind), predicate: func(s *subject) error {
		diagnostic := fmt.Errorf("no recorded await of kind %s", kind)
		for _, event := range s.events {
			if event.Type != awaitEventType(kind) {
				continue
			}
			if !event.verified {
				return fmt.Errorf("evidence unverified: %s", event.Type)
			}
			item, err := decodeAwait(event.ObservedEvent)
			if err != nil {
				return err
			}
			if predicate == nil {
				return nil
			}
			if err := predicate(item); err != nil {
				diagnostic = err
			} else {
				return nil
			}
		}
		return diagnostic
	}}
}

// awaitEventType selects evidence without decoding unrelated observed events.
func awaitEventType(kind planner.AwaitItemKind) hooks.EventType {
	switch kind {
	case planner.AwaitItemKindClarification:
		return hooks.AwaitClarification
	case planner.AwaitItemKindQuestions:
		return hooks.AwaitQuestions
	case planner.AwaitItemKindTypedInput:
		return hooks.AwaitTypedInput
	case planner.AwaitItemKindExternalTools:
		return hooks.AwaitExternalTools
	default:
		return ""
	}
}

// decodeAwait restores planner prompt shapes from recorded hook payloads. The
// external-tool hook uses ToolName where the planner's item uses Name.
func decodeAwait(event *trajectory.ObservedEvent) (planner.AwaitItem, error) {
	var item planner.AwaitItem
	var target any
	switch event.Type {
	case hooks.AwaitClarification:
		item = planner.AwaitClarificationItem(&planner.AwaitClarification{})
		target = item.Clarification
	case hooks.AwaitQuestions:
		item = planner.AwaitQuestionsItem(&planner.AwaitQuestions{})
		target = item.Questions
	case hooks.AwaitTypedInput:
		item = planner.AwaitTypedInputItem(&planner.AwaitTypedInput{})
		target = item.TypedInput
	case hooks.AwaitExternalTools:
		var external hooks.AwaitExternalToolsEvent
		if err := json.Unmarshal(event.Payload, &external); err != nil {
			return item, fmt.Errorf("decode await: %q", err.Error())
		}
		items := make([]planner.AwaitToolItem, len(external.Items))
		for i, call := range external.Items {
			items[i] = planner.AwaitToolItem{Name: call.ToolName, ToolCallID: call.ToolCallID, Payload: call.Payload}
		}
		return planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: external.ID, Items: items}), nil
	case hooks.RunStarted, hooks.RunCompleted, hooks.RunPaused,
		hooks.AwaitConfirmation, hooks.ToolAuthorization, hooks.RunResumed,
		hooks.ToolCallScheduled, hooks.ToolCallArgsDelta, hooks.ToolResultReceived,
		hooks.ToolCallUpdated, hooks.PlannerNote, hooks.ThinkingBlock,
		hooks.AssistantMessage, hooks.AssistantTurnCommitted, hooks.RetryHintIssued,
		hooks.MemoryAppended, hooks.PolicyDecision, hooks.Usage,
		hooks.HardProtectionTriggered, hooks.RunPhaseChanged, hooks.ChildRunLinked,
		hooks.PromptRendered, hooks.PlannerStep, hooks.RunSealed,
		hooks.ContextCompacted, hooks.ContextCompactionSkipped,
		hooks.ContextOverflow, hooks.ContextRequestGrowth,
		hooks.ContextUnmeasured, hooks.ContextLimitsUnknown, hooks.ContextBudgetTooSmall,
		hooks.ContextRecalled, hooks.ContextRecallDisabled, hooks.RecallBudgetExhausted:
		// These events do not carry a planner AwaitItem.
		return item, nil
	default:
		return item, nil
	}
	if err := json.Unmarshal(event.Payload, target); err != nil {
		return item, fmt.Errorf("decode await: %q", err.Error())
	}
	return item, nil
}
