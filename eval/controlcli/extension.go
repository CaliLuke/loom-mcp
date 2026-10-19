// Package controlcli adds explicitly bound experiment controls to an eval CLI.
package controlcli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/control"
	"github.com/CaliLuke/loom-mcp/v2/eval/evalcli"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// BindService binds controls to the invocation's selected experiment store.
type BindService func(context.Context, *eval.Store) (*control.Service, error)

// Extensions returns the optional experiment command without installing it globally.
func Extensions(bind BindService) ([]evalcli.Extension, error) {
	if bind == nil {
		return nil, control.NewError(control.InvalidInput, "", "", "")
	}
	extension := evalcli.Extension{Name: "experiment", Help: "experiment <operation> [--request <JSON-object>]"}
	extension.Run = func(ctx context.Context, request evalcli.ExtensionRequest) error {
		operation, payload, err := parseRequest(request.Args)
		if err != nil {
			return err
		}
		service, err := bind(ctx, request.Store)
		if err != nil {
			return safeError(err)
		}
		registration, err := control.NewToolsetRegistration(control.ToolsetConfig{Service: service})
		if err != nil {
			return safeError(err)
		}
		result, err := registration.Execute(ctx, &planner.ToolRequest{Name: tools.Ident("experiment." + string(operation)), ToolCallID: "cli", Payload: payload})
		if err != nil {
			return safeError(err)
		}
		if result.ToolResult.Error != nil {
			var failure control.Error
			if err := json.Unmarshal([]byte(result.ToolResult.Error.Message), &failure); err != nil {
				return safeError(err)
			}
			return control.NewError(failure.Code, "", "", failure.Field)
		}
		data, err := json.Marshal(result.ToolResult.Result)
		if err != nil {
			return safeError(err)
		}
		_, err = fmt.Fprintln(request.Stdout, string(data))
		return safeError(err)
	}
	return []evalcli.Extension{extension}, nil
}

// parseRequest accepts one exact operation and an optional closed JSON-object payload.
func parseRequest(args []string) (control.Operation, rawjson.Message, error) {
	if len(args) == 0 || !knownOperation(control.Operation(args[0])) {
		return "", nil, control.NewError(control.InvalidInput, "", "", "")
	}
	operation := control.Operation(args[0])
	if len(args) == 1 && operation == control.DiscoverOperation {
		return operation, rawjson.Message(`{}`), nil
	}
	if len(args) != 3 || args[1] != "--request" {
		return "", nil, control.NewError(control.InvalidInput, "", "", "")
	}
	return operation, rawjson.Message(args[2]), nil
}

func knownOperation(operation control.Operation) bool {
	switch operation {
	case control.PreviewCopyOperation, control.CopyOperation, control.DiscoverOperation, control.CreateOperation, control.GetOperation, control.ReadOperation, control.EditOperation, control.ValidateOperation, control.FreezeOperation, control.SubmitOperation, control.StatusOperation, control.CancelOperation, control.ListEvidenceOperation, control.ReadEvidenceOperation, control.CompareOperation:
		return true
	default:
		return false
	}
}

// safeError keeps all host diagnostics out of the CLI's public error envelope.
func safeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var failure *control.Error
	if errors.As(err, &failure) {
		return control.NewError(failure.Code, "", "", failure.Field)
	}
	return control.NewError(control.OperationFailed, "", "", "")
}
