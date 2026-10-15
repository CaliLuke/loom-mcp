package callrecord

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type requestFaultStore struct {
	runstore.Store
	putAt, getAt, puts, gets int
}

func TestBuildRequestPropagatesStorageFailuresWithoutUsableRecord(t *testing.T) {
	for _, operation := range []string{"put", "get"} {
		for at := 1; at <= 4; at++ {
			t.Run(fmt.Sprintf("%s/%d", operation, at), func(t *testing.T) {
				store := &requestFaultStore{Store: inmem.New()}
				if operation == "put" {
					store.putAt = at
				} else {
					store.getAt = at
				}
				parts, sizes, err := BuildRequest(t.Context(), store, "root", &model.Request{Messages: []*model.Message{recordMessage("input")}}, index.ViewRef{}, nil)
				require.ErrorIs(t, err, assert.AnError)
				assert.Empty(t, parts)
				assert.Empty(t, sizes)
			})
		}
	}
	parts, _, err := BuildRequest(t.Context(), inmem.New(), "root", nil, index.ViewRef{}, nil)
	require.ErrorContains(t, err, "request is required")
	assert.Empty(t, parts)
}

func TestRebuildRejectsCorruptRequestComponents(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, runstore.Store, *RequestParts)
		want   string
	}{
		{"missing parameters", func(_ *testing.T, _ runstore.Store, p *RequestParts) {
			p.Params = "absent"
		}, "read request part"},
		{"invalid parameters", func(t *testing.T, s runstore.Store, p *RequestParts) {
			p.Params = putRequestBytes(t, s, `{`)
		}, "decode request part"},
		{"missing tools", func(_ *testing.T, _ runstore.Store, p *RequestParts) {
			p.Tools = "absent"
		}, "read recorded tools"},
		{"invalid tool catalog", func(t *testing.T, s runstore.Store, p *RequestParts) {
			p.Tools = putRequestBytes(t, s, `{}`)
		}, "decode recorded tools"},
		{"invalid tool entry", func(t *testing.T, s runstore.Store, p *RequestParts) {
			p.Tools = putRequestBytes(t, s, `[42]`)
		}, "decode recorded tool"},
		{"missing message", func(_ *testing.T, _ runstore.Store, p *RequestParts) {
			p.Messages[0].Hash = "absent"
		}, "read request message"},
		{"malformed message", func(t *testing.T, s runstore.Store, p *RequestParts) {
			p.Messages[0].Hash = putRequestBytes(t, s, `{`)
		}, "decode recorded message"},
		{"mixed hash and span", func(_ *testing.T, _ runstore.Store, p *RequestParts) {
			p.Messages[0].ViewTo = 1
		}, "hash segment has view positions"},
		{"missing view", func(_ *testing.T, _ runstore.Store, p *RequestParts) {
			p.Messages = []MessageSegment{{ViewTo: 1}}
		}, "invalid view span"},
		{"missing system", func(_ *testing.T, _ runstore.Store, p *RequestParts) {
			p.System = "absent"
		}, "read recorded system"},
		{"changed system", func(t *testing.T, s runstore.Store, p *RequestParts) {
			p.System = putRequestBytes(t, s, `[]`)
		}, "rebuild mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := inmem.New()
			system := &model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "instruction"}}}
			parts, _, err := BuildRequest(t.Context(), store, "root", &model.Request{Messages: []*model.Message{system, recordMessage("input")}}, index.ViewRef{}, nil)
			require.NoError(t, err)
			tt.change(t, store, &parts)
			req, err := Rebuild(t.Context(), store, "root", Started{Request: parts}, nil)
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, req)
		})
	}
}

func TestRebuildRequiresRecordedViewAndValidSpans(t *testing.T) {
	view := []*model.Message{recordMessage("recorded")}
	tests := []struct {
		name   string
		change func(*RequestParts)
		loader ViewLoader
		want   string
	}{
		{"no loader", func(*RequestParts) {
		}, nil, "committed view and loader"},
		{"pending entries", func(p *RequestParts) {
			p.View.Pending = []index.MessageEntry{{ID: 1}}
		}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
			return view, nil
		}, "committed view and loader"},
		{"loader fails", func(*RequestParts) {
		}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
			return nil, assert.AnError
		}, "load recorded view"},
		{"negative span", func(p *RequestParts) {
			p.Messages[0].ViewFrom = -1
		}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
			return view, nil
		}, "invalid view span"},
		{"empty span", func(p *RequestParts) {
			p.Messages[0].ViewTo = 0
		}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
			return view, nil
		}, "invalid view span"},
		{"span exceeds view", func(p *RequestParts) {
			p.Messages[0].ViewTo = 2
		}, func(context.Context, index.ViewRef) ([]*model.Message, error) {
			return view, nil
		}, "invalid view span"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := inmem.New()
			parts, _, err := BuildRequest(t.Context(), store, "root", &model.Request{Messages: view}, index.ViewRef{IndexHead: "recorded"}, view)
			require.NoError(t, err)
			tt.change(&parts)
			req, err := Rebuild(t.Context(), store, "root", Started{Request: parts}, tt.loader)
			require.ErrorContains(t, err, tt.want)
			assert.Nil(t, req)
		})
	}
}

func (s *requestFaultStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	s.puts++
	if s.putAt == s.puts {
		return "", assert.AnError
	}
	return s.Store.Put(ctx, scope, data)
}

func (s *requestFaultStore) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	s.gets++
	if s.getAt == s.gets {
		return nil, assert.AnError
	}
	return s.Store.Get(ctx, scope, hash)
}

func putRequestBytes(t *testing.T, s runstore.Store, data string) runstore.Hash {
	t.Helper()
	hash, err := s.Put(t.Context(), "root", []byte(data))
	require.NoError(t, err)
	return hash
}
