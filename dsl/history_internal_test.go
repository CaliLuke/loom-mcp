package dsl

import (
	"testing"

	agentexpr "github.com/CaliLuke/loom-mcp/expr/agent"
	"github.com/CaliLuke/loom/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressHistoryRejectsUnknownMode(t *testing.T) {
	eval.Reset()
	t.Cleanup(eval.Reset)

	history := &agentexpr.HistoryExpr{Mode: "archive"}
	var got *agentexpr.HistoryExpr
	executed := eval.Execute(func() {
		got = compressHistory("KeepMaxTurns")
	}, history)

	assert.False(t, executed)
	assert.Nil(t, got)
	require.Contains(t, eval.Context.Error(), `unknown history policy mode "archive"`)
}
