// Package activity preserves the agent adapter's plain JSON payload contract.
package activity

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/CaliLuke/loom/execution/adapter"
)

// Handler binds a validated activity to the agent adapter's JSON codec.
type Handler struct {
	handler adapter.Handler
	input   reflect.Type
}

// New validates the concrete handler before building the JSON bridge.
func New(fn any) (Handler, error) {
	handler, err := adapter.New(fn)
	if err != nil {
		return Handler{}, err
	}
	return Handler{handler: handler, input: reflect.TypeOf(fn).In(1)}, nil
}

// CallJSON preserves the backend's plain JSON payload format without an envelope.
func (h Handler) CallJSON(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	input := reflect.New(h.input)
	if err := json.Unmarshal(data, input.Interface()); err != nil {
		return nil, err
	}
	out, err := h.handler.Call(ctx, input.Elem().Interface())
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
