package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// workflowContentBatch is transient, one-step state. Hashes may be used while
// preparing dependent metadata, but nothing is adopted until flush verifies all
// returned hashes. Items are deduplicated in deterministic first-use order.
type workflowContentBatch struct {
	items  [][]byte
	hashes []runstore.Hash
	seen   map[runstore.Hash]bool
}

type workflowContentKey struct{}

type contentWorkflowContext struct {
	engine.WorkflowContext
	batch *workflowContentBatch
}

func contentBatchFromContext(ctx context.Context) *workflowContentBatch {
	batch, _ := ctx.Value(workflowContentKey{}).(*workflowContentBatch)
	return batch
}

func (w contentWorkflowContext) Context() context.Context {
	// Preserve the native engine identity required by Temporal Await.
	return context.WithValue(w.WorkflowContext.Context(), workflowContentKey{}, w.batch)
}

func (w contentWorkflowContext) WithCancel() (engine.WorkflowContext, func()) {
	child, cancel := w.WorkflowContext.WithCancel()
	return contentWorkflowContext{WorkflowContext: child, batch: w.batch}, cancel
}

func (b *workflowContentBatch) add(items [][]byte) []runstore.Hash {
	refs := make([]runstore.Hash, len(items))
	if b.seen == nil {
		b.seen = make(map[runstore.Hash]bool)
	}
	for i, data := range items {
		hash := runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
		refs[i] = hash
		if b.seen[hash] {
			continue
		}
		b.seen[hash] = true
		b.items = append(b.items, bytes.Clone(data))
		b.hashes = append(b.hashes, hash)
	}
	return refs
}

func (b *workflowContentBatch) input(root string) *api.RecordContentInput {
	return &api.RecordContentInput{RootRunID: root, Items: b.items}
}

func (b *workflowContentBatch) verify(out *api.RecordContentOutput) error {
	if out == nil || len(out.Refs) != len(b.hashes) {
		return fmt.Errorf("content batch returned invalid reference count")
	}
	for i, hash := range b.hashes {
		if out.Refs[i] != hash {
			return fmt.Errorf("content batch returned incorrect hash at item %d", i)
		}
	}
	return nil
}

func (b *workflowContentBatch) flush(ctx context.Context, id agent.Ident, root string) error {
	if len(b.items) == 0 {
		return nil
	}
	wf := engine.WorkflowContextFromContext(ctx)
	if wf == nil {
		return fmt.Errorf("content batch requires workflow context")
	}
	out, err := wf.ExecuteRecordContentActivity(ctx, engine.RecordContentActivityCall{Name: RecordContentActivityName(id), Input: b.input(root)})
	if err != nil {
		return err
	}
	return b.verify(out)
}
