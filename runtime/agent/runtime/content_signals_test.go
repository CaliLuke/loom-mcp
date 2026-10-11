package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Signal tests use a real in-memory execution and inspect its typed receiver
// after the client returns, avoiding timing-based claims that no signal arrived.
func TestContentSignalsRecordBeforeDelivery(t *testing.T) {
	for _, runID := range []string{"root", "child"} {
		for _, kind := range []string{"resume", "clarification", "tools", "typed"} {
			for _, failAt := range []int{0, 1, -1} {
				t.Run(fmt.Sprintf("%s/%s/fail=%d", runID, kind, failAt), func(t *testing.T) {
					store := &contentPathStore{Store: storeinmem.New(), failAt: failAt}
					eng := engineinmem.New()
					rt := New(WithEngine(eng), WithRunStore(store))
					ready := make(chan engine.WorkflowContext, 1)
					inspect := make(chan struct{})
					received := make(chan any, 1)
					require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "signals", Handler: func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
						if err := w.SetQueryHandler(api.QueryRootRunID, func() (string, error) {
							if failAt == -1 {
								return "", errors.New("scope unavailable")
							}
							return "root", nil
						}); err != nil {
							return nil, err
						}
						ready <- w
						<-inspect
						var value any
						switch kind {
						case "resume":
							v, ok := w.ResumeRequests().ReceiveAsync()
							if ok {
								value = v
							}
						case "clarification":
							v, ok := w.ClarificationAnswers().ReceiveAsync()
							if ok {
								value = v
							}
						case "tools":
							v, ok := w.ExternalToolResults().ReceiveAsync()
							if ok {
								value = v
							}
						case "typed":
							v, ok := w.TypedInputAnswers().ReceiveAsync()
							if ok {
								value = v
							}
						}
						received <- value
						return &api.RunOutput{}, nil
					}}))
					handle, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: runID, Workflow: "signals", Input: &api.RunInput{RootRunID: "root"}})
					require.NoError(t, err)
					<-ready
					var want []byte
					switch kind {
					case "resume":
						msg := contentMessage("resume")
						want, err = json.Marshal(msg)
						require.NoError(t, err)
						err = rt.ResumeRun(t.Context(), &api.ResumeRequest{RunID: runID, Messages: []*model.Message{msg}, MessageSizes: []contextmeasure.MessageSize{{Bytes: 999, NonText: 999, Role: model.ConversationRoleSystem}}})
					case "clarification":
						want = []byte("answer")
						err = rt.ProvideClarification(t.Context(), &api.ClarificationAnswer{RunID: runID, Answer: "answer", AnswerBytes: new(999)})
					case "tools":
						want = []byte(`{"ok":true}`)
						err = rt.ProvideToolResults(t.Context(), &api.ToolResultsSet{RunID: runID, Results: []*api.ProvidedToolResult{{Name: "test.tool", Result: rawjson.Message(want)}}})
					case "typed":
						want = []byte(`{"approved":true}`)
						err = rt.ProvideTypedInput(t.Context(), &api.TypedInputAnswer{RunID: runID, Payload: rawjson.Message(want)})
					}
					close(inspect)
					value := <-received
					_, waitErr := handle.Wait(t.Context())
					require.NoError(t, waitErr)
					if failAt == -1 {
						require.ErrorContains(t, err, "scope unavailable")
						assert.Nil(t, value)
						assert.Empty(t, store.snapshot())
						return
					}
					if failAt > 0 {
						require.ErrorIs(t, err, runstore.ErrContentTooLarge)
						assert.Nil(t, value)
						return
					}
					require.NoError(t, err)
					require.NotNil(t, value)
					writes := store.snapshot()
					require.Len(t, writes, 1)
					assert.Equal(t, "root", writes[0].scope)
					assert.Equal(t, want, writes[0].data)
					switch v := value.(type) {
					case *api.ResumeRequest:
						assert.Empty(t, v.Messages)
						assert.Equal(t, []contextmeasure.MessageSize{{Bytes: 6, Role: model.ConversationRoleUser}}, v.MessageSizes)
						assert.Equal(t, []runstore.Hash{writes[0].hash}, v.MessageRefs)
					case *api.ClarificationAnswer:
						assert.Empty(t, v.Answer)
						assert.Equal(t, new(6), v.AnswerBytes)
						assert.Equal(t, writes[0].hash, v.AnswerRef)
					case *api.ToolResultsSet:
						assert.Empty(t, v.Results[0].Result)
						assert.Equal(t, writes[0].hash, v.Results[0].Refs.Result)
					case *api.TypedInputAnswer:
						assert.Empty(t, v.Payload)
						assert.Equal(t, writes[0].hash, v.PayloadRef)
					}
				})
			}
		}
	}
}

func TestContentSignalQueryFailureDoesNotWrite(t *testing.T) {
	store := &contentPathStore{Store: storeinmem.New()}
	rt := New(WithEngine(engineinmem.New()), WithRunStore(store))
	for _, send := range []func(context.Context) error{
		func(ctx context.Context) error {
			return rt.ResumeRun(ctx, &api.ResumeRequest{RunID: "missing", Messages: []*model.Message{contentMessage("x")}})
		},
		func(ctx context.Context) error {
			return rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "missing", Answer: "x"})
		},
		func(ctx context.Context) error {
			return rt.ProvideToolResults(ctx, &api.ToolResultsSet{RunID: "missing", Results: []*api.ProvidedToolResult{{Result: rawjson.Message(`{}`)}}})
		},
		func(ctx context.Context) error {
			return rt.ProvideTypedInput(ctx, &api.TypedInputAnswer{RunID: "missing", Payload: rawjson.Message(`{}`)})
		},
	} {
		require.Error(t, send(t.Context()))
	}
	assert.Empty(t, store.snapshot())
}
