package enginetest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

type signalCase struct {
	name, signal string
	payload      any
	receive      func(engine.WorkflowContext) (any, error)
}

func signalScenarios() []scenario {
	cases := []signalCase{
		{"pause", api.SignalPause, &api.PauseRequest{RunID: testRunID, Reason: "operator", RequestedBy: "user", Labels: map[string]string{labelKey: labelValue}},
			func(w engine.WorkflowContext) (any, error) {
				return w.PauseRequests().Receive(w.Context())
			}},
		{"resume", api.SignalResume, &api.ResumeRequest{RunID: testRunID, Notes: "continue", RequestedBy: "user", Labels: map[string]string{labelKey: labelValue}},
			func(w engine.WorkflowContext) (any, error) {
				return w.ResumeRequests().Receive(w.Context())
			}},
		{"clarification", api.SignalProvideClarification, &api.ClarificationAnswer{RunID: testRunID, ID: "question", Answer: "answer", AnswerRef: "sha256:answer"},
			func(w engine.WorkflowContext) (any, error) {
				return w.ClarificationAnswers().Receive(w.Context())
			}},
		{"external_tool_results", api.SignalProvideToolResults, &api.ToolResultsSet{RunID: testRunID, ID: "external", Results: []*api.ProvidedToolResult{{Name: echoTool, ToolCallID: testCallID, Result: rawjson.Message(`{"ok":true}`)}}},
			func(w engine.WorkflowContext) (any, error) {
				return w.ExternalToolResults().Receive(w.Context())
			}},
		{"confirmation", api.SignalProvideConfirmation, &api.ConfirmationDecision{RunID: testRunID, ID: "confirmation", Approved: true, Labels: map[string]string{labelKey: labelValue}},
			func(w engine.WorkflowContext) (any, error) {
				return w.ConfirmationDecisions().Receive(w.Context())
			}},
		{"typed_answer", api.SignalProvideTypedInput, &api.TypedInputAnswer{RunID: testRunID, ID: "typed", PayloadRef: "sha256:typed", Payload: rawjson.Message(`{"answer":[1,true]}`)},
			func(w engine.WorkflowContext) (any, error) {
				return w.TypedInputAnswers().Receive(w.Context())
			}},
	}
	result := make([]scenario, 0, len(cases))
	for _, s := range cases {
		result = append(result, scenario{name: "signals/" + s.name, run: func(t *testing.T, f Fixture) error {
			return testSignal(t, f, s)
		}})
	}
	return result
}

func testSignal(t *testing.T, f Fixture, s signalCase) error {
	ctx := f.ctx
	entered, release := activityBarrier(t, f, true)
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		if err := announce(w); err != nil {
			return nil, err
		}
		got, err := s.receive(w)
		if err != nil {
			return nil, err
		}
		return success(), checkEqual(s.payload, got)
	})
	h := start(t, f, ctx)
	receive(t, ctx, entered)
	require.NoError(t, h.Signal(ctx, s.signal, s.payload))
	release()
	return waitSuccess(ctx, h)
}
