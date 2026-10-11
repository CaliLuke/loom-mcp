package runtime

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resultReadStore struct {
	runstore.Store
	result runstore.Hash
	reads  int
}

func (s *resultReadStore) Get(ctx context.Context, root string, hash runstore.Hash) ([]byte, error) {
	if hash == s.result {
		s.reads++
	}
	return s.Store.Get(ctx, root, hash)
}

// The index must produce the legacy canonical projection without reading a
// result that the projection omits. Explicit planner loads still read its bytes.
func TestViewLoadsOnlyInlineResultContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bytes   int
		preview string
		bounds  *agent.Bounds
	}{
		{name: "at_limit", bytes: transcript.MaxToolResultContentBytes},
		{name: "over_limit", bytes: transcript.MaxToolResultContentBytes + 1},
		{name: "over_limit_with_metadata", bytes: 100 * 1024, preview: "summary", bounds: &agent.Bounds{Returned: 10, Truncated: true, RefinementHint: "narrow query"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &resultReadStore{Store: storeinmem.New()}
			rt := New(WithRunStore(store))
			data := rawjson.Message(`"` + strings.Repeat("x", tc.bytes-2) + `"`)
			hash, err := store.Put(t.Context(), "root", data)
			require.NoError(t, err)
			store.result = hash
			output := planner.ToolOutput{Name: "test.result", ToolCallID: "call", Refs: tools.ResultRefs{Result: hash}, Preview: tc.preview, Bounds: tc.bounds}
			encoded, err := json.Marshal(output)
			require.NoError(t, err)
			outputHash, err := store.Put(t.Context(), "root", encoded)
			require.NoError(t, err)
			base := &workflowPlanInput{RunContext: run.Context{RunID: "run", RootRunID: "root"}}
			require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.ToolResult, Hash: hash, Output: outputHash, Bytes: len(data), Batch: 1, ToolUseIDs: []string{"call"}, Result: &index.ResultProjection{Preview: index.TextParameter{Text: tc.preview}, Bounds: tc.bounds}}))
			view, err := rt.loadTestView(t.Context(), "root", "run", base.view())
			require.NoError(t, err)
			if tc.bytes > transcript.MaxToolResultContentBytes {
				assert.Zero(t, store.reads, "omitted content must not be fetched, even for reference validation")
			} else {
				assert.Positive(t, store.reads, "inline content must be fetched")
			}
			content, err := transcript.ProjectToolResultContent(data, tc.bounds, tc.preview, "")
			require.NoError(t, err)
			want := []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: content}}}}
			assert.Equal(t, canonicalIndexMessages(t, want), canonicalIndexMessages(t, view.Messages))
			require.Len(t, view.Outputs, 1)
			before := store.reads
			loaded, err := (viewContent{store: store, scope: "root"}).LoadToolResult(t.Context(), view.Outputs[0].Refs)
			require.NoError(t, err)
			assert.Equal(t, data, loaded)
			assert.Equal(t, before+1, store.reads)
		})
	}
}
