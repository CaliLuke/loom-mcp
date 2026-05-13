// Package runtime owns execution-time result preview rendering.
//
// This file defines the template contract for `ResultHintTemplate(...)`.
//
// Contract:
//   - `Result` is the semantic typed tool result returned by the executor.
//   - `Args` is the semantic typed tool payload when it can be decoded.
//   - `Bounds` is the runtime-owned bounded-result metadata when present.
//   - Preview rendering does not reshape semantic results or synthesize fields.
package runtime

import (
	"context"
	"strings"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	rthints "github.com/CaliLuke/loom-mcp/runtime/agent/runtime/hints"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

type (
	// resultPreviewTemplateData is the explicit template root passed to
	// ResultHintTemplate renderers.
	//
	// `Result` carries the semantic typed result as returned by the tool executor.
	// `Args` exposes the typed call payload when available. `Bounds` exposes
	// runtime-owned bounded-result metadata when the tool declared BoundedResult
	// and the executor returned bounds; otherwise it remains nil.
	resultPreviewTemplateData struct {
		Result any
		Args   any
		Bounds *agent.Bounds
	}
)

// formatResultPreview renders the user-facing tool result preview for toolName.
//
// This must run while the tool result is still strongly typed (i.e. before the
// hook event crosses the hook-activity JSON boundary). The helper passes an
// explicit template root so ResultHintTemplate authors reference semantic data
// via `.Result`, typed payloads via `.Args`, and runtime-owned bounds via
// `.Bounds`.
func formatResultPreview(toolName tools.Ident, result any, bounds *agent.Bounds) string {
	return clampPreview(rthints.FormatResultHint(toolName, resultPreviewTemplateData{
		Result: result,
		Bounds: bounds,
	}))
}

func (r *Runtime) formatResultPreview(ctx context.Context, toolName tools.Ident, payload rawjson.Message, result any, bounds *agent.Bounds) string {
	return clampPreview(rthints.FormatResultHint(toolName, resultPreviewTemplateData{
		Result: result,
		Args:   r.decodeResultPreviewArgs(ctx, toolName, payload),
		Bounds: bounds,
	}))
}

func (r *Runtime) decodeResultPreviewArgs(ctx context.Context, toolName tools.Ident, payload rawjson.Message) any {
	if len(payload) == 0 {
		return nil
	}
	args, err := r.unmarshalToolValue(ctx, toolName, payload.RawMessage(), true)
	if err != nil {
		return nil
	}
	return args
}

// clampPreview normalizes whitespace and bounds previews to a reasonable length
// for UI display. It mirrors the normalization logic used by stream subscribers
// so callers can safely pass previews through hook events unchanged.
func clampPreview(in string) string {
	if in == "" {
		return ""
	}
	// normalize whitespace
	out := make([]rune, 0, len(in))
	prevSpace := false
	for _, r := range in {
		switch r {
		case '\n', '\r', '\t', ' ':
			if !prevSpace {
				out = append(out, ' ')
			}
			prevSpace = true
		default:
			out = append(out, r)
			prevSpace = false
		}
	}
	const max = 140
	if len(out) <= max {
		return strings.TrimSpace(string(out))
	}
	return strings.TrimSpace(string(out[:max]))
}
