package callrecord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// ViewLoader resolves the recorded ViewRef, never the run's current view.
type ViewLoader func(context.Context, index.ViewRef) ([]*model.Message, error)

// BuildRequest stores content, encodes spans against the pre-policy view, and
// verifies the rebuild. Unstable messages and a mismatching rebuild use hashes.
func BuildRequest(ctx context.Context, store runstore.Store, scope string, req *model.Request, ref index.ViewRef, view []*model.Message) (RequestParts, MessageBytes, error) {
	if req == nil {
		return RequestParts{}, MessageBytes{}, fmt.Errorf("callrecord request is required")
	}
	if len(ref.Pending) != 0 {
		return RequestParts{}, MessageBytes{}, fmt.Errorf("callrecord view must be committed")
	}
	parts, err := storeRequestFields(ctx, store, scope, req)
	if err != nil {
		return RequestParts{}, MessageBytes{}, err
	}
	parts.Messages, err = encodeSegments(ctx, store, scope, req.Messages, view)
	if err != nil {
		return RequestParts{}, MessageBytes{}, err
	}
	for _, segment := range parts.Messages {
		if segment.Hash == "" {
			owned := ref
			parts.View = &owned
			break
		}
	}
	started := Started{Request: parts, Model: req.Model, Class: req.ModelClass, Prompts: req.PromptRefs}
	loader := func(context.Context, index.ViewRef) ([]*model.Message, error) {
		return view, nil
	}
	if _, err := Rebuild(ctx, store, scope, started, loader); err != nil {
		// Only a verification mismatch warrants replacing spans; store failures fail unsent.
		if !errors.Is(err, ErrRequestMismatch) {
			return RequestParts{}, MessageBytes{}, err
		}
		parts.Messages, err = encodeSegments(ctx, store, scope, req.Messages, nil)
		if err != nil {
			return RequestParts{}, MessageBytes{}, err
		}
		parts.View = nil
		started.Request = parts
		if _, err := Rebuild(ctx, store, scope, started, loader); err != nil {
			return RequestParts{}, MessageBytes{}, fmt.Errorf("verify hashed request: %w", err)
		}
	}
	sizes, err := messageSizes(req.Messages, parts.Messages)
	return parts, sizes, err
}

// RequestDigest computes the verification-only hash of the canonical request.
// The complete encoding is never stored as addressed content.
func RequestDigest(req *model.Request) (runstore.Hash, error) {
	data, err := Encode(req)
	if err != nil {
		return "", err
	}
	return runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data))), nil
}

// Rebuild resolves all parts and verifies the full exported request encoding.
// The unexported completion validator is intentionally outside this format.
func Rebuild(ctx context.Context, store runstore.Store, scope string, started Started, load ViewLoader) (*model.Request, error) {
	parts := started.Request
	var params Params
	if err := readJSON(ctx, store, scope, parts.Params, &params); err != nil {
		return nil, err
	}
	tools, err := readTools(ctx, store, scope, parts.Tools)
	if err != nil {
		return nil, err
	}
	messages, err := rebuildMessages(ctx, store, scope, parts, load)
	if err != nil {
		return nil, err
	}
	req := &model.Request{RunID: params.RunID, Model: started.Model, ModelClass: started.Class, PromptRefs: started.Prompts, Messages: messages, Temperature: params.Temperature, Tools: tools, ToolChoice: params.ToolChoice, MaxTokens: params.MaxTokens, StructuredOutput: params.StructuredOutput, Stream: params.Stream, Thinking: params.Thinking, Cache: params.Cache}
	actual, err := RequestDigest(req)
	if err != nil {
		return nil, err
	}
	if actual != parts.Request {
		return nil, ErrRequestMismatch
	}
	system, err := Encode(systemMessages(messages))
	if err != nil {
		return nil, err
	}
	recordedSystem, err := store.Get(ctx, scope, parts.System)
	if err != nil {
		return nil, fmt.Errorf("read recorded system: %w", err)
	}
	if !bytes.Equal(system, recordedSystem) {
		return nil, ErrRequestMismatch
	}
	return req, nil
}

func storeRequestFields(ctx context.Context, store runstore.Store, scope string, req *model.Request) (RequestParts, error) {
	params := Params{RunID: req.RunID, Temperature: req.Temperature, ToolChoice: req.ToolChoice, MaxTokens: req.MaxTokens, StructuredOutput: req.StructuredOutput, Stream: req.Stream, Thinking: req.Thinking, Cache: req.Cache}
	var parts RequestParts
	for _, field := range []struct {
		value any
		hash  *runstore.Hash
	}{
		{systemMessages(req.Messages), &parts.System}, {req.Tools, &parts.Tools}, {params, &parts.Params},
	} {
		data, err := Encode(field.value)
		if err != nil {
			return RequestParts{}, err
		}
		hash, err := store.Put(ctx, scope, data)
		if err != nil {
			return RequestParts{}, fmt.Errorf("store request part: %w", err)
		}
		*field.hash = hash
	}
	var err error
	parts.Request, err = RequestDigest(req)
	if err != nil {
		return RequestParts{}, err
	}
	return parts, nil
}

// encodeSegments extends only the immediately preceding consecutive view span.
func encodeSegments(ctx context.Context, store runstore.Store, scope string, messages, view []*model.Message) ([]MessageSegment, error) {
	positions := make(map[string]int, len(view))
	encodedView := make([][]byte, len(view))
	for position, message := range view {
		data, err := Encode(message)
		if err != nil {
			return nil, err
		}
		encodedView[position] = data
		if _, exists := positions[string(data)]; !exists {
			positions[string(data)] = position
		}
	}
	segments := make([]MessageSegment, 0, len(messages))
	for _, message := range messages {
		data, err := Encode(message)
		if err != nil {
			return nil, err
		}
		position, matches := segmentPosition(data, encodedView, positions, segments)
		n := len(segments)
		if matches && messageStable(data) {
			if n > 0 && segments[n-1].Hash == "" && segments[n-1].ViewTo == position {
				segments[n-1].ViewTo++
			} else {
				segments = append(segments, MessageSegment{ViewFrom: position, ViewTo: position + 1})
			}
		} else {
			hash, err := store.Put(ctx, scope, data)
			if err != nil {
				return nil, fmt.Errorf("store request message: %w", err)
			}
			segments = append(segments, MessageSegment{Hash: hash})
		}
	}
	return segments, nil
}

// segmentPosition prefers the next position so repeated view messages stay in place.
func segmentPosition(data []byte, view [][]byte, positions map[string]int, segments []MessageSegment) (int, bool) {
	n := len(segments)
	if n > 0 && segments[n-1].Hash == "" {
		next := segments[n-1].ViewTo
		if next < len(view) && bytes.Equal(data, view[next]) {
			return next, true
		}
	}
	position, matches := positions[string(data)]
	return position, matches
}

func rebuildMessages(ctx context.Context, store runstore.Store, scope string, parts RequestParts, load ViewLoader) ([]*model.Message, error) {
	var view []*model.Message
	if parts.View != nil {
		if len(parts.View.Pending) != 0 || load == nil {
			return nil, fmt.Errorf("callrecord requires a committed view and loader")
		}
		var err error
		view, err = load(ctx, *parts.View)
		if err != nil {
			return nil, fmt.Errorf("load recorded view: %w", err)
		}
	}
	var messages []*model.Message
	for _, segment := range parts.Messages {
		if segment.Hash != "" {
			if segment.ViewFrom != 0 || segment.ViewTo != 0 {
				return nil, fmt.Errorf("callrecord hash segment has view positions")
			}
			data, err := store.Get(ctx, scope, segment.Hash)
			if err != nil {
				return nil, fmt.Errorf("read request message: %w", err)
			}
			message, err := decodeMessage(data)
			if err != nil {
				return nil, err
			}
			messages = append(messages, message)
		} else {
			if parts.View == nil || segment.ViewFrom < 0 || segment.ViewTo <= segment.ViewFrom || segment.ViewTo > len(view) {
				return nil, fmt.Errorf("callrecord invalid view span [%d,%d)", segment.ViewFrom, segment.ViewTo)
			}
			messages = append(messages, view[segment.ViewFrom:segment.ViewTo]...)
		}
	}
	return messages, nil
}

func messageSizes(messages []*model.Message, segments []MessageSegment) (MessageBytes, error) {
	var sizes MessageBytes
	position := 0
	for _, segment := range segments {
		count := segment.ViewTo - segment.ViewFrom
		if segment.Hash != "" {
			count = 1
		}
		for range count {
			data, err := Encode(messages[position])
			if err != nil {
				return MessageBytes{}, err
			}
			if segment.Hash == "" {
				sizes.Inside += len(data)
			} else {
				sizes.Outside += len(data)
			}
			position++
		}
	}
	return sizes, nil
}

func systemMessages(messages []*model.Message) []*model.Message {
	var system []*model.Message
	for _, message := range messages {
		if message != nil && message.Role == model.ConversationRoleSystem {
			system = append(system, message)
		}
	}
	return system
}

func readJSON(ctx context.Context, store runstore.Store, scope string, hash runstore.Hash, value any) error {
	data, err := store.Get(ctx, scope, hash)
	if err != nil {
		return fmt.Errorf("read request part: %w", err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode request part: %w", err)
	}
	return nil
}
