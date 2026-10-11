package agenttest

import (
	"errors"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// RawStub declares a stub over raw JSON for a tool without a typed descriptor.
// Successful JSON results are decoded through the registered result codec before
// the runtime's ordinary AfterTool, materialization and encoding path.
func RawStub(h *Harness, name tools.Ident) *ToolStub[rawjson.Message, rawjson.Message] {
	codec := tools.JSONCodec[rawjson.Message]{
		ToJSON: func(value rawjson.Message) ([]byte, error) {
			return value.MarshalJSON()
		},
		FromJSON: func(data []byte) (rawjson.Message, error) {
			var value rawjson.Message
			if err := value.UnmarshalJSON(data); err != nil {
				return nil, err
			}
			return value, nil
		},
	}
	stub := Stub(h, tools.TypedTool[rawjson.Message, rawjson.Message]{Name: name, Payload: codec, Result: codec})
	spec, ok := h.rt.ToolSpec(name)
	if !ok {
		return stub
	}
	if spec.Result.Codec.FromJSON == nil {
		h.stubSetupError(errors.New("agenttest: RawStub requires a registered result decoder"))
		return stub
	}
	h.stubs.mu.Lock()
	stub.resultValue = func(result rawjson.Message) (any, error) {
		return spec.Result.Codec.FromJSON(slices.Clone(result))
	}
	h.stubs.mu.Unlock()
	return stub
}

// PassthroughAll allows real execution of every activity tool without a stub.
// Explicit stubs still take precedence, including when their responses exhaust.
func PassthroughAll() Option {
	return func(options *harnessOptions) error {
		options.passthroughAll = true
		return nil
	}
}

// Passthrough allows real execution of the named activity tools without stubs.
// Calls are recorded with Passthrough true, raw arguments and correlation IDs;
// their real outcomes remain available in the runtime trajectory.
func (h *Harness) Passthrough(tools ...tools.Named) {
	h.stubs.mu.Lock()
	defer h.stubs.mu.Unlock()
	for _, tool := range tools {
		h.stubs.passthrough[tool.Ident()] = true
	}
}
