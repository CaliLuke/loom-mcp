package enginetest

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

type metadataInput struct {
	Key  string
	Tags map[string]string
}

type metadataOutput struct {
	Size        int64
	ContentType string
}

// Generic activities exercise the same options, retries and failure matrix as
// built-in agent activities, with application-owned payloads.
func genericActivityCase(async bool) activityCase {
	name := "custom"
	if async {
		name += "_async"
	}
	activity := engine.Activity[metadataInput, metadataOutput]{Name: activityName}
	return activityCase{
		name:   name,
		input:  metadataInput{Key: "uploads/report", Tags: map[string]string{"owner": "workspace"}},
		output: metadataOutput{Size: 42, ContentType: "text/plain"},
		register: func(ctx context.Context, e engine.Engine, opts engine.ActivityOptions, fn func(context.Context, any) (any, error)) error {
			return activity.Register(ctx, e, opts, func(ctx context.Context, in metadataInput) (metadataOutput, error) {
				return typedResult[metadataOutput](fn(ctx, in))
			})
		},
		invoke: func(w engine.WorkflowContext, in any, opts engine.ActivityOptions) (any, error) {
			if async {
				future, err := activity.ExecuteAsync(w.Context(), w, in.(metadataInput), opts)
				if err != nil {
					return nil, err
				}
				out, err := future.Get(w.Context())
				if err != nil {
					return out, err
				}
				if !future.IsReady() {
					return nil, errors.New("completed activity future is not ready")
				}
				repeated, err := future.Get(w.Context())
				if err != nil {
					return nil, err
				}
				return out, checkEqual(out, repeated)
			}
			return activity.Execute(w.Context(), w, in.(metadataInput), opts)
		},
	}
}
