package dsl

import (
	"github.com/CaliLuke/loom/eval"

	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	runtimetools "github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// Idempotent emits metadata declaring a tool idempotent within a run transcript.
//
// The built-in runtime does not compare arguments, cache results, suppress
// duplicate execution, or provide exactly-once delivery. External planners or
// orchestrators may consume the metadata to implement their own replay policy.
//
// Use Idempotent only for tools whose result is a pure function of their
// arguments *for the lifetime of a run transcript*. If a tool answers questions
// about changing state (for example, "current mode") but does not accept a time
// or version parameter, it is not transcript-idempotent and should not be
// tagged.
//
// Default: no transcript-idempotency metadata is emitted.
func Idempotent() {
	tool, ok := eval.Current().(*agentsexpr.ToolExpr)
	if !ok {
		incompatibleDSL("Idempotent")
		return
	}
	tool.Tags = append(tool.Tags, runtimetools.TagIdempotencyTranscript)
}
