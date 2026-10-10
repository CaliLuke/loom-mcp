package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCallInfoContext(t *testing.T) {
	base := context.Background()
	_, ok := CallInfoFrom(base)
	assert.False(t, ok)
	want := CallInfo{AgentID: "agent", RunID: "run", RootRunID: "root", SessionID: "session", TurnID: "turn", ModelID: "model", Seq: 3, Attempt: 2}
	ctx := WithCallInfo(base, want)
	got, ok := CallInfoFrom(ctx)
	assert.True(t, ok)
	assert.Equal(t, want, got)
	_, ok = CallInfoFrom(base)
	assert.False(t, ok)
}
