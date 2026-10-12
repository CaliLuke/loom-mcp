package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// recordClarificationContent joins text and question without introducing adjacent
// assistant roles. Content loading and measurement remain outside the workflow.
func (r *Runtime) recordClarificationContent(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	clarification := input.Clarification
	data, err := r.RunStore.Get(ctx, input.RootRunID, clarification.TranscriptRef)
	if err != nil {
		return nil, err
	}
	var messages []*model.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	if len(messages) == 0 || messages[len(messages)-1] == nil || messages[len(messages)-1].Role != model.ConversationRoleAssistant {
		return nil, fmt.Errorf("clarification transcript must end with an assistant message")
	}
	last := messages[len(messages)-1]
	last.Parts = append(last.Parts, model.TextPart{Text: clarification.Question})
	size, err := contextmeasure.MeasureMessages(messages)
	if err != nil {
		return nil, err
	}
	data, err = json.Marshal(messages, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	ref, err := r.RunStore.Put(ctx, input.RootRunID, data)
	if err != nil {
		return nil, err
	}
	return &api.RecordContentOutput{Entries: []index.MessageEntry{{Kind: index.Assistant, Hash: ref, Bytes: size.Bytes, NonText: size.NonText, Role: model.ConversationRoleAssistant}}}, nil
}

// appendClarificationTranscript uses the question's existing content activity to
// retain the await-only transcript as part of the same immutable assistant turn.
func appendClarificationTranscript(ctx context.Context, base *workflowPlanInput, question string, answer runstore.Hash, bytes *int, transcript runstore.Hash) error {
	if bytes == nil || *bytes < 0 {
		return fmt.Errorf("clarification measurement is required and must be non-negative")
	}
	if *bytes == 0 {
		if err := base.recordAssistant(transcript, nil); err != nil {
			return err
		}
		return appendClarificationAnswer(base, answer, bytes)
	}
	wf := engine.WorkflowContextFromContext(ctx)
	if wf == nil {
		return fmt.Errorf("clarification requires workflow context")
	}
	out, err := wf.ExecuteRecordContentActivity(ctx, engine.RecordContentActivityCall{
		Name:  RecordContentActivityName(base.AgentID),
		Input: &api.RecordContentInput{RootRunID: base.RunContext.RootRunID, Clarification: &api.ClarificationContent{TranscriptRef: transcript, Question: question}},
	})
	if err != nil {
		return err
	}
	if out == nil || len(out.Entries) != 1 {
		return fmt.Errorf("clarification content must return one assistant entry")
	}
	entry := out.Entries[0]
	entry.Call = base.producingCall
	if err := base.appendEntry(entry); err != nil {
		return err
	}
	return appendClarificationAnswer(base, answer, bytes)
}
