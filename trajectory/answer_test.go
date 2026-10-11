package trajectory_test

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/CaliLuke/loom-mcp/v2/trajectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAnswerContentIsReadableJSON(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    index.EntryKind
		source  string
		content string
		encoded string
	}{
		{"clarification text", index.Clarification, "  choose \"café\"\nnext\tline  ", `"  choose \"café\"\nnext\tline  "`, `"  choose \"café\"\nnext\tline  "`},
		{"JSON-looking clarification is text", index.Clarification, `{"large":9007199254740993}`, `"{\"large\":9007199254740993}"`, `"{\"large\":9007199254740993}"`},
		{"empty clarification", index.Clarification, "", `""`, `""`},
		{"typed object", index.TypedInput, " { \"large\":9007199254740993, \"fraction\":1.2300, \"exponent\":1e+09 } ", " { \"large\":9007199254740993, \"fraction\":1.2300, \"exponent\":1e+09 } ", `{"exponent":1e+09,"fraction":1.2300,"large":9007199254740993}`},
		{"typed array", index.TypedInput, `[9007199254740993,{"nested":-9007199254740995}]`, `[9007199254740993,{"nested":-9007199254740995}]`, `[9007199254740993,{"nested":-9007199254740995}]`},
		{"typed number", index.TypedInput, `9007199254740993`, `9007199254740993`, `9007199254740993`},
		{"typed string", index.TypedInput, `"typed answer"`, `"typed answer"`, `"typed answer"`},
		{"typed boolean", index.TypedInput, `true`, `true`, `true`},
		{"typed null", index.TypedInput, `null`, `null`, `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture()
			head := f.segment(t, "", []index.MessageEntry{{ID: 1, Kind: test.kind, Hash: f.putBytes(t, []byte(test.source))}})
			f.event(t, hooks.NewRunSealedEvent("run", head, api.WorkflowProtocolVersion, time.Unix(1, 0)), 1)
			rec, err := runrecord.Read(t.Context(), f.store, f.log, "root", "run")
			require.NoError(t, err)
			got, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.NoError(t, err)
			require.Len(t, got.Steps, 1)
			answer := got.Steps[0].Answer
			require.NotNil(t, answer)
			assert.IsType(t, json.RawMessage(nil), answer.Content)
			assert.Equal(t, test.content, string(answer.Content))
			assert.Equal(t, []byte(test.source), rec.Content[rec.Runs[0].Entries[0].Hash])
			data, err := got.Marshal()
			require.NoError(t, err)
			var wire struct {
				Steps []struct {
					Answer struct {
						Content json.RawMessage `json:"content"`
					} `json:"answer"`
				} `json:"steps"`
			}
			require.NoError(t, jsonv2.Unmarshal(data, &wire))
			require.Len(t, wire.Steps, 1)
			assert.Equal(t, test.encoded, string(wire.Steps[0].Answer.Content))
		})
	}
}

func TestBuildReplacesInvalidClarificationUTF8(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		text   string
	}{
		{"separate invalid sequences", "before \xff after \xc3(", "before � after �("},
		{"consecutive invalid bytes", "\xff\xfe", "��"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Build's record boundary permits raw text without rendering a transcript.
			rec := &runrecord.RunRecord{
				Runs:    []runrecord.Run{{RunID: "run", Sealed: true, Entries: []index.MessageEntry{{ID: 1, Kind: index.Clarification, Hash: "text"}}}},
				Content: map[runstore.Hash][]byte{"text": []byte(test.source)},
			}
			got, err := trajectory.Build(rec, "run", trajectory.Options{})
			require.NoError(t, err)
			require.Len(t, got.Steps, 1)
			var text string
			require.NoError(t, jsonv2.Unmarshal(got.Steps[0].Answer.Content, &text))
			assert.Equal(t, test.text, text)
			assert.Equal(t, []byte(test.source), rec.Content["text"])
			data, err := got.Marshal()
			require.NoError(t, err)
			assert.Contains(t, string(data), test.text)
		})
	}
}
