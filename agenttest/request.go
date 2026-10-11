package agenttest

import (
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Request is an immutable model request with optional capture provenance.
// Transforms defer errors until Err, Request, MarshalRequest or Decide is used.
type Request struct {
	data    []byte
	modelID string
	source  *Source
	err     error
}

// Source identifies the captured model call.
type Source struct {
	RunID   string `json:"run_id"`
	Seq     int    `json:"seq"`
	Attempt int    `json:"attempt"`
}

// NewRequest snapshots req without its private completion validator. Fixtures
// contain full prompts; provider thinking signatures are not portable for replay.
func NewRequest(modelID string, req *model.Request) (Request, error) {
	if req == nil {
		return Request{}, errors.New("agenttest: request is nil")
	}
	data, err := callrecord.Encode(req)
	if err != nil {
		return Request{}, err
	}
	return Request{data: data, modelID: modelID}, nil
}

// ModelID returns the registered model ID, rather than the provider model name.
func (r Request) ModelID() string {
	return r.modelID
}

// Source returns an owned copy of the captured call key, or nil for a fixture
// constructed without capture provenance.
func (r Request) Source() *Source {
	if r.source == nil {
		return nil
	}
	source := *r.source
	return &source
}

// Request decodes a fresh owned request, excluding the private completion
// validator. Provider thinking signatures must not be replayed across providers.
func (r Request) Request() (*model.Request, error) {
	if r.err != nil {
		return nil, r.err
	}
	if len(r.data) == 0 {
		return nil, errors.New("agenttest: request is empty")
	}
	return callrecord.DecodeRequest(r.data)
}

// Err returns any deferred transform error.
func (r Request) Err() error {
	return r.err
}

// WithUserMessage replaces the last user message without tool results.
func (r Request) WithUserMessage(text string) Request {
	return r.transform(func(req *model.Request) error {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			message := req.Messages[i]
			if message == nil || message.Role != model.ConversationRoleUser {
				continue
			}
			hasResult := false
			for _, part := range message.Parts {
				if _, ok := part.(model.ToolResultPart); ok {
					hasResult = true
					break
				}
			}
			if !hasResult {
				req.Messages[i] = textMessage(model.ConversationRoleUser, text)
				return nil
			}
		}
		return errors.New("agenttest: WithUserMessage: no eligible user message")
	})
}

// WithSystem replaces all system messages at the first system position, or
// inserts the system message at the beginning when none exists.
func (r Request) WithSystem(text string) Request {
	return r.transform(func(req *model.Request) error {
		messages := make([]*model.Message, 0, len(req.Messages)+1)
		inserted := false
		for _, message := range req.Messages {
			if message != nil && message.Role == model.ConversationRoleSystem {
				if !inserted {
					messages = append(messages, textMessage(model.ConversationRoleSystem, text))
					inserted = true
				}
			} else {
				messages = append(messages, message)
			}
		}
		if !inserted {
			messages = append([]*model.Message{textMessage(model.ConversationRoleSystem, text)}, messages...)
		}
		req.Messages = messages
		return nil
	})
}

// WithToolSpecs replaces definitions by name and appends new names in order.
// Other definitions, including tool_unavailable, remain in the request.
func (r Request) WithToolSpecs(specs ...tools.ToolSpec) Request {
	return r.transform(func(req *model.Request) error {
		for _, spec := range specs {
			var schema any = map[string]any{"type": "object"}
			if len(spec.Payload.Schema) > 0 {
				var err error
				schema, err = model.DecodeSchemaJSON(spec.Payload.Schema)
				if err != nil {
					return fmt.Errorf("agenttest: tool %q schema: %w", spec.Name, err)
				}
			}
			definition := &model.ToolDefinition{Name: spec.Name.String(), Description: spec.Description, InputSchema: schema}
			replaced := false
			for i, old := range req.Tools {
				if old != nil && old.Name == definition.Name {
					req.Tools[i] = definition
					replaced = true
					break
				}
			}
			if !replaced {
				req.Tools = append(req.Tools, definition)
			}
		}
		return nil
	})
}

// WithModel sets the provider-specific model name without changing ModelID.
func (r Request) WithModel(provider string) Request {
	return r.transform(func(req *model.Request) error {
		req.Model = provider
		return nil
	})
}

// WithRunID sets the model request's run ID.
func (r Request) WithRunID(id string) Request {
	return r.transform(func(req *model.Request) error {
		req.RunID = id
		return nil
	})
}

func textMessage(role model.ConversationRole, text string) *model.Message {
	return &model.Message{Role: role, Parts: []model.Part{model.TextPart{Text: text}}}
}

// transform always edits a decoded copy, retaining the first deferred error.
func (r Request) transform(edit func(*model.Request) error) Request {
	req, err := r.Request()
	if err == nil {
		err = edit(req)
	}
	if err == nil {
		r.data, err = callrecord.Encode(req)
	}
	r.err = err
	return r
}
