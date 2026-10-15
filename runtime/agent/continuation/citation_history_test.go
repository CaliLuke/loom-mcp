package continuation

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	contentmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessionmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCitationReadsAuthorizedEarlierRun(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		deny, missingHead, corrupt bool
		id                         index.MessageID
		wantFound                  bool
		wantErr                    bool
	}{
		{name: "original user line", id: 1, wantFound: true},
		{name: "zero identifier", id: 0},
		{name: "outside index", id: 2},
		{name: "authorization failure", id: 1, deny: true, wantErr: true},
		{name: "head unavailable", id: 1, missingHead: true, wantErr: true},
		{name: "index corrupt", id: 1, corrupt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := sessionmem.New()
			_, err := sessions.CreateSession(t.Context(), "session", time.Unix(1, 0))
			require.NoError(t, err)
			require.NoError(t, sessions.UpsertRun(t.Context(), session.RunMeta{RunID: "first", AgentID: "agent", SessionID: "session", Status: session.RunStatusCompleted}))
			_, err = sessions.AdvanceContinuation(t.Context(), "session", nil, session.Continuation{ID: "first", Pos: 1})
			require.NoError(t, err)
			content := contentmem.New()
			raw, err := json.Marshal(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "original evidence\nsecond line"}}})
			require.NoError(t, err)
			hash, err := content.Put(t.Context(), "first", raw)
			require.NoError(t, err)
			segment, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: hash}})
			require.NoError(t, err)
			encoded, _, err := index.EncodeSegment(segment)
			require.NoError(t, err)
			if tc.corrupt {
				encoded = []byte("{")
			}
			head, err := content.Put(t.Context(), "first", encoded)
			require.NoError(t, err)
			chain := &Chain{RunID: "second", SessionID: "session", Prev: new("first"), Pos: 2, Sessions: sessions, Continuations: sessions, Content: content,
				Authorize: func(_ context.Context, access recall.Access) error {
					assert.Equal(t, recall.Access{SessionID: "session", RunID: "second", TargetRunID: "first", Scope: "first"}, access)
					if tc.deny {
						return assert.AnError
					}
					return nil
				},
				HeadOf: func(_ context.Context, run, scope string) (runstore.Hash, error) {
					assert.Equal(t, "first", run)
					assert.Equal(t, "first", scope)
					if tc.missingHead {
						return "", assert.AnError
					}
					return head, nil
				},
			}
			resolver := chain.Citations(t.Context(), nil, nil, 0, nil)
			target, found, err := resolver.Resolve(checkpoint.OrderKey{Pos: 1, ID: tc.id})
			if tc.wantErr || tc.corrupt {
				require.Error(t, err)
				if tc.wantErr {
					require.ErrorIs(t, err, assert.AnError)
				}
				assert.False(t, found)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantFound, found)
			if tc.wantFound {
				assert.Equal(t, index.Message, target.Kind)
				assert.Equal(t, "original evidence", target.Line.Text)
				assert.Equal(t, &index.Origin{Scope: "first", Run: "first", Pos: 1, ID: 1, Form: "full"}, target.Line.Origin)
				assert.True(t, resolver.Behind(checkpoint.OrderKey{Pos: 1, ID: 1}))
			}
		})
	}
}
