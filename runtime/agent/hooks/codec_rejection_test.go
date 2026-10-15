package hooks_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeRejectsMalformedPayloadForEveryEventFamily(t *testing.T) {
	for _, kind := range []hooks.EventType{hooks.RunStarted, hooks.RunCompleted, hooks.RunPaused, hooks.AwaitClarification, hooks.AwaitQuestions, hooks.AwaitTypedInput, hooks.AwaitConfirmation, hooks.AwaitExternalTools, hooks.ToolAuthorization, hooks.RunResumed, hooks.ToolCallScheduled, hooks.ToolCallArgsDelta, hooks.ToolResultReceived, hooks.ToolCallUpdated, hooks.PlannerNote, hooks.ThinkingBlock, hooks.AssistantMessage, hooks.AssistantTurnCommitted, hooks.RetryHintIssued, hooks.MemoryAppended, hooks.PolicyDecision, hooks.Usage, hooks.HardProtectionTriggered, hooks.RunPhaseChanged, hooks.ChildRunLinked, hooks.PromptRendered, hooks.ContextCompatibility, hooks.ContextNonTextResident, hooks.ContextUnmeasured, hooks.ContextLimitsUnknown, hooks.ContextBudgetTooSmall, hooks.ContextRecalled, hooks.ContextRecallDisabled, hooks.RecallBudgetExhausted, hooks.ContextResidencyAdopted, hooks.ContinuationStarted, hooks.ContinuationPublished, hooks.ContinuationSuperseded, hooks.PlannerStep, hooks.RunSealed, hooks.ContextCompacted, hooks.ContextCompactionSkipped, hooks.ContextOverflow, hooks.ContextRequestGrowth} {
		t.Run(string(kind), func(t *testing.T) {
			event, err := hooks.DecodeFromHookInput(&hooks.ActivityInput{Type: kind, RunID: "run", Payload: rawjson.Message(`{`)})
			require.Error(t, err)
			assert.Nil(t, event)
		})
	}
}

func TestEncodeRejectsUnserializableToolResults(t *testing.T) {
	input, err := hooks.EncodeToHookInput(&hooks.ToolResultReceivedEvent{Result: make(chan int)}, "turn")
	require.Error(t, err)
	assert.Nil(t, input)
}
