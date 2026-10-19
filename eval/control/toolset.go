package control

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"unicode"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type nativeTool struct {
	spec    tools.ToolSpec
	execute func(context.Context, []byte) (any, error)
}

// NewToolsetRegistration exposes only explicitly granted control operations.
func NewToolsetRegistration(cfg ToolsetConfig) (agentsruntime.ToolsetRegistration, error) {
	if cfg.Service == nil {
		return agentsruntime.ToolsetRegistration{}, invalid("")
	}
	name := cfg.Name
	if name == "" {
		name = "experiment"
	}
	if !toolsetName(name) {
		return agentsruntime.ToolsetRegistration{}, invalid("")
	}
	entries, err := nativeTools(name, cfg.Service)
	if err != nil {
		return agentsruntime.ToolsetRegistration{}, err
	}
	reg := agentsruntime.ToolsetRegistration{Name: name, Description: "Create immutable candidates, submit host-owned trials and inspect authorized evidence.", DecodeInExecutor: true}
	handlers := make(map[tools.Ident]nativeTool, len(entries))
	for _, entry := range entries {
		handlers[entry.spec.Name] = entry
		reg.Specs = append(reg.Specs, entry.spec)
	}
	reg.Execute = func(ctx context.Context, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
		if call == nil {
			return nil, NewError(InvalidInput, "", "", "")
		}
		entry, ok := handlers[call.Name]
		if !ok {
			return nativeFailure(call, NewError(Unsupported, "", "", "")), nil
		}
		out, err := entry.execute(ctx, call.Payload.RawMessage())
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			safe := publicError(err, OperationFailed, "")
			var ce *Error
			if !errors.As(safe, &ce) {
				return nil, safe
			}
			return nativeFailure(call, ce), nil
		}
		return agentsruntime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: out}), nil
	}
	return reg, nil
}

func nativeTools(name string, s *Service) ([]nativeTool, error) {
	var out []nativeTool
	for _, op := range s.cfg.Operations {
		tool, err := nativeToolFor(name, op, s)
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	return out, nil
}

// nativeToolFor binds one closed typed operation without changing service policy.
func nativeToolFor(name string, op Operation, s *Service) (nativeTool, error) {
	switch op {
	case DiscoverOperation:
		return makeTool(name, op, s, func(ctx context.Context, _ struct{}) (Discovery, error) {
			return s.Discover(ctx)
		})
	case CreateOperation:
		return makeTool(name, op, s, s.Create)
	case GetOperation:
		return makeTool(name, op, s, s.GetCandidate)
	case ReadOperation:
		return makeTool(name, op, s, s.ReadCandidate)
	case EditOperation:
		return makeTool(name, op, s, s.Edit)
	case PreviewCopyOperation:
		return makeTool(name, op, s, s.PreviewCopy)
	case CopyOperation:
		return makeTool(name, op, s, s.Copy)
	case ValidateOperation:
		return makeTool(name, op, s, s.Validate)
	case FreezeOperation:
		return makeTool(name, op, s, s.Freeze)
	case SubmitOperation:
		return makeTool(name, op, s, s.Submit)
	case StatusOperation:
		return makeTool(name, op, s, s.Status)
	case CancelOperation:
		return makeTool(name, op, s, s.Cancel)
	case ListEvidenceOperation:
		return makeTool(name, op, s, s.ListEvidence)
	case ReadEvidenceOperation:
		return makeTool(name, op, s, s.ReadEvidence)
	case CompareOperation:
		return makeTool(name, op, s, s.Compare)
	default:
		return nativeTool{}, invalid(jsonOperation)
	}
}

func makeTool[P, R any](name string, op Operation, s *Service, run func(context.Context, P) (R, error)) (nativeTool, error) {
	payload, err := json.Marshal(s.nativeRequestSchema(op))
	if err != nil {
		return nativeTool{}, err
	}
	result, err := json.Marshal(resultSchema(op))
	if err != nil {
		return nativeTool{}, err
	}
	spec := tools.ToolSpec{Name: tools.Ident(name + "." + string(op)), Toolset: name, Description: description(op), Payload: tools.TypeSpec{Schema: payload, Codec: typedCodec[P](payload)}, Result: tools.TypeSpec{Schema: result, Codec: typedCodec[R](result)}}
	return nativeTool{spec: spec, execute: func(ctx context.Context, raw []byte) (any, error) {
		value, err := spec.Payload.Codec.FromJSON(raw)
		if err != nil {
			return nil, err
		}
		input, ok := value.(P)
		if !ok {
			return nil, NewError(OperationFailed, "", "", "")
		}
		return run(ctx, input)
	}}, nil
}

func typedCodec[T any](schema []byte) tools.JSONCodec[any] {
	return tools.JSONCodec[any]{
		FromJSON: func(raw []byte) (any, error) {
			var instance any
			if err := json.Unmarshal(raw, &instance); err != nil {
				return nil, invalid("")
			}
			if err := tools.ValidateJSONSchema(schema, instance); err != nil {
				return nil, invalid("")
			}
			var out T
			if err := json.Unmarshal(raw, &out, json.RejectUnknownMembers(true)); err != nil {
				return nil, invalid("")
			}
			return out, nil
		},
		ToJSON: func(value any) ([]byte, error) {
			typed, ok := value.(T)
			if !ok {
				return nil, NewError(OperationFailed, "", "", "")
			}
			return json.Marshal(typed)
		},
	}
}
func nativeFailure(call *planner.ToolRequest, e *Error) *agentsruntime.ToolExecutionResult {
	reason := planner.RetryReasonInvalidArguments
	switch e.Code {
	case InvalidInput, RevisionConflict, FrozenConflict, NotFound, RequestConflict:
		reason = planner.RetryReasonInvalidArguments
	case Unsupported:
		reason = planner.RetryReasonUnsupportedOperation
	case ExecutorUnavailable, EvidenceNotReady, OperationFailed:
		reason = planner.RetryReasonToolUnavailable
	}
	return agentsruntime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError(e.Error()), RetryHint: &planner.RetryHint{Reason: reason, Tool: call.Name, Message: e.Recovery}})
}
func toolsetName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return !strings.HasPrefix(s, ".") && !strings.HasSuffix(s, ".")
}
func description(op Operation) string {
	switch op {
	case DiscoverOperation:
		return "Discover authorized baseline, editable slots, limits, tasks and available operations."
	case PreviewCopyOperation:
		return "Preview the complete selected source dependency closure and every destination collision without mutation. Inspect the entire plan before copy_candidate; oversized plans fail without truncation."
	case CopyOperation:
		return "Copy exactly a previously previewed closure using its plan_digest and explicit replace list. A changed source, destination revision or confirmation requires a fresh preview. This operation rechecks current read and edit grants."
	case CreateOperation:
		return "Create an editable candidate from the discovered baseline or an authorized frozen candidate."
	case GetOperation:
		return "Read candidate component metadata and the current draft revision for optimistic edits."
	case ReadOperation:
		return "Read bounded exact candidate bytes using a draft revision or frozen digest; data is base64."
	case EditOperation:
		return "Edit one component at expected_revision. Put exactly one of content (UTF-8 text) or content_base64. Rejected edits preserve the revision; repair and retry."
	case ValidateOperation:
		return "Validate the expected draft revision; repair the indicated component on rejection."
	case FreezeOperation:
		return "Freeze a valid draft revision before submitting; frozen content cannot be edited."
	case SubmitOperation:
		return "Submit a frozen candidate to an authorized task, default probe. Reuse the same request_key and inputs after an uncertain response; changed inputs require a new key."
	case StatusOperation:
		return "Poll a host-owned job; cancel_requested does not confirm stopped execution."
	case CancelOperation:
		return "Request cancellation; poll until the host confirms a terminal state after finalization."
	case ListEvidenceOperation:
		return "List bounded evidence entries from the host-authorized exact job view."
	case ReadEvidenceOperation:
		return "Read bounded evidence bytes through a job-local locator; data is base64 and missing/withheld/provisional are explicit."
	case CompareOperation:
		return "Compare exact authorized terminal trials: use baseline_job_id/candidate_job_id or bounded baseline_job_ids/candidate_job_ids lists. Groups pool repeats over shared cases; unmatched cases are excluded from totals. Probes have no comparable quality verdict."
	default:
		return "Unsupported control operation."
	}
}
