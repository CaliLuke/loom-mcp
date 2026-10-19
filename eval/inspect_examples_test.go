package eval_test

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

// PublishEditorView publishes an immutable editor view from host-owned attempt snapshots.
// snippet:start inspect-publish-editor
func PublishEditorView(
	ctx context.Context,
	dir string,
	experiments *eval.Store,
	candidates *candidate.Store,
	origin inspect.Origin,
	candidateDigest string,
	declarations []eval.Deliverable,
	source []byte,
) (*inspect.View, error) {
	workspace, err := inspect.Open(dir, experiments, candidates, inspect.DefaultLimits())
	if err != nil {
		return nil, err
	}
	ref, err := workspace.Publish(ctx, inspect.Publication{
		Origin:               origin,
		CandidateDigest:      candidateDigest,
		ExpectedDeliverables: declarations,
		Attachments: []inspect.Attachment{{
			Name: "task-source.txt", Kind: inspect.TaskSource,
			MediaType: "text/plain", Data: source,
			Audiences: []inspect.Role{inspect.Editor},
		}},
		Policies: []inspect.Policy{{Role: inspect.Editor, Name: "application.editor.v1"}},
	})
	if err != nil {
		return nil, err
	}
	return workspace.View(
		inspect.Scope{Manifests: []inspect.ManifestRef{ref}},
		inspect.ViewPolicy{Role: inspect.Editor, Name: "application.editor.v1"},
	)
}

// snippet:end inspect-publish-editor
