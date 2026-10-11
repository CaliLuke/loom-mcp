package eval

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreFingerprintSortedUnion(t *testing.T) {
	rec := &runrecord.RunRecord{RootRunID: "root", Runs: []runrecord.Run{{RunID: "root"}}}
	for _, id := range []string{"z", "a", "z"} {
		rec.Runs[0].Calls = append(rec.Runs[0].Calls, runrecord.Call{Started: callrecord.Started{ModelID: id, Class: model.ModelClass("reasoning"), Prompts: []prompt.PromptRef{{ID: prompt.Ident(id), Version: "1"}}, Request: callrecord.RequestParts{System: runstore.Hash(id), Tools: "tool"}}})
	}
	got, err := recordFingerprint(rec)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "z"}, got.Models["reasoning"])
	assert.Equal(t, []prompt.PromptRef{{ID: "a", Version: "1"}, {ID: "z", Version: "1"}}, got.Prompts)
	assert.Equal(t, []string{"a", "z"}, got.SystemHashes)
	assert.Equal(t, []string{"tool"}, got.ToolHashes)
	require.Len(t, got.Context, 1)
	assert.Contains(t, got.Context[0], "sha256:")
	union := mergeFingerprint(got, got)
	assert.Equal(t, got, union)
}
