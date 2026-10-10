package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// LoadRunToolEvents loads the event list addressed by RunOutput.ToolEventsRef
// and restores eligible Result and server-only ServerData bytes. Scope must be
// the root run ID, including when loading a child run's events. Missing list or
// content hashes fail with wrapped store errors. Callers must authorize access
// to the scope; returned events include server-only data.
func LoadRunToolEvents(ctx context.Context, store runstore.Store, scope string, ref runstore.Hash) ([]*api.ToolEvent, error) {
	if store == nil {
		return nil, fmt.Errorf("load run tool events: run store is required")
	}
	if ref == "" {
		return nil, fmt.Errorf("load run tool events: %w: empty hash", runstore.ErrNotFound)
	}
	data, err := store.Get(ctx, scope, ref)
	if err != nil {
		return nil, fmt.Errorf("load run tool events: %w", err)
	}
	var events []*api.ToolEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode run tool events: %w", err)
	}
	for _, event := range events {
		if event == nil {
			return nil, fmt.Errorf("load run tool events: nil event")
		}
		event.Result, event.ServerData, err = loadToolContent(ctx, store, scope, event.Name, event.Refs, event.Result, event.ServerData)
		if err != nil {
			return nil, fmt.Errorf("load run tool events: %w", err)
		}
		if event.Refs.Result != "" {
			event.ResultBytes = len(event.Result)
		}
	}
	return events, nil
}

// loadToolContent restores only referenced parts, preserving inline content.
func loadToolContent(ctx context.Context, store runstore.Store, scope string, name tools.Ident, refs tools.ResultRefs, result, serverData rawjson.Message) (rawjson.Message, rawjson.Message, error) {
	values := []rawjson.Message{result, serverData}
	for i, part := range []struct {
		name string
		hash runstore.Hash
	}{{"result", refs.Result}, {"server data", refs.ServerData}} {
		if part.hash == "" {
			values[i] = append(rawjson.Message(nil), values[i]...)
			continue
		}
		if store == nil {
			return nil, nil, fmt.Errorf("load tool %s %s: run store is required", name, part.name)
		}
		data, err := store.Get(ctx, scope, part.hash)
		if err != nil {
			return nil, nil, fmt.Errorf("load tool %s %s: %w", name, part.name, err)
		}
		values[i] = rawjson.Message(data)
	}
	return values[0], values[1], nil
}
