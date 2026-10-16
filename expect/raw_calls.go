package expect

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
)

// RawCall matches the first later named call satisfying raw JSON predicates.
// Arguments must be valid JSON even when args is nil. A non-nil result predicate
// requires a verified successful execution with a recorded JSON result; it is not
// applicable to Calls. A nil result predicate checks arguments only and does not
// assert execution success. Predicates receive independent copies of the bytes.
// Prefer Call when a generated typed descriptor is available.
func RawCall(tool tools.Named, args func(rawjson.Message) error, result func(rawjson.Message) error) Expectation {
	var execution func(*trajectory.ToolStep) error
	if result != nil {
		execution = func(step *trajectory.ToolStep) error {
			if step.Error != nil {
				return fmt.Errorf("tool failed: %s", step.Error.Message)
			}
			if len(step.Result) == 0 {
				return fmt.Errorf("missing result")
			}
			return rawPredicate(result, "result")(step.Result)
		}
	}
	return callExpectation{name: "raw_call " + tool.Ident().String(), tool: tool.Ident(), match: rawPredicate(args, "arguments"), execution: execution, verifiedExecution: result != nil}
}

// RawFailed matches the first later verified failed execution of a named tool.
// Arguments must be valid JSON even when args is nil. A nil failure predicate
// accepts any recorded tool error. RawFailed is not applicable to Calls.
// The argument predicate receives an independent copy of the JSON bytes.
// Prefer Failed when a generated typed descriptor is available.
func RawFailed(tool tools.Named, args func(rawjson.Message) error, failure func(*planner.ToolError) error) Expectation {
	return callExpectation{name: "raw_failed " + tool.Ident().String(), tool: tool.Ident(), match: rawPredicate(args, "arguments"), failedOnly: true, verifiedExecution: true, execution: func(step *trajectory.ToolStep) error {
		if step.Error == nil {
			return fmt.Errorf("call to %s did not fail", tool.Ident())
		}
		if failure != nil {
			return failure(step.Error)
		}
		return nil
	}}
}

// rawPredicate validates JSON without decoding numbers or normalizing its bytes.
func rawPredicate(predicate func(rawjson.Message) error, kind string) func([]byte) error {
	return func(data []byte) error {
		if !jsontext.Value(data).IsValid() {
			return fmt.Errorf("malformed JSON %s", kind)
		}
		if predicate != nil {
			return predicate(rawjson.Message(bytes.Clone(data)))
		}
		return nil
	}
}
