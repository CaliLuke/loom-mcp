package eval_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatasetRoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/valid.jsonl")
	require.NoError(t, err)
	d, err := eval.DecodeJSONL(bytes.NewReader(data), "valid.jsonl")
	require.NoError(t, err)
	assert.Equal(t, "valid.jsonl", d.Source())
	assert.Equal(t, 2, d.Len())
	assert.JSONEq(t, `{"n":9007199254740993,"decimal":1.2300e+04}`, string(d.Items()[0].Data))
	assert.Contains(t, string(d.Items()[0].Data), "1.2300e+04")
	assert.Contains(t, string(d.Items()[0].ExpectedTools.Tools[0].Args), "9007199254740993")
	var buf bytes.Buffer
	require.NoError(t, eval.EncodeJSONL(&buf, d))
	assert.Equal(t, string(data), buf.String())
	again, err := eval.DecodeJSONL(&buf, "again")
	require.NoError(t, err)
	assert.Equal(t, d.Items(), again.Items())
	assert.Equal(t, d.Hash(), again.Hash())
}

func TestDatasetHashAndOwnership(t *testing.T) {
	original := eval.Item{ID: "case", Turns: []string{"hello"}, Data: json.RawMessage(`{"z":9007199254740993,"a":1}`), ExpectedTools: &expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: "echo", Args: json.RawMessage(`{"z":2,"a":1}`)}}}}
	d := eval.Items(original)
	require.NoError(t, d.Err())
	original.Turns[0] = "mutated"
	original.Data[0] = '['
	original.ExpectedTools.Tools[0].Name = "mutated"
	copyItems := d.Items()
	copyItems[0].Turns[0] = "other"
	copyItems[0].Data[0] = '['
	copyItems[0].ExpectedTools.Tools[0].Args[0] = '['
	assert.Equal(t, "hello", d.Items()[0].Turns[0])
	assert.Equal(t, tools.Ident("echo"), d.Items()[0].ExpectedTools.Tools[0].Name)
	assert.Equal(t, "go", d.Source())
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, d.Hash())
	text := "{\"version\":1, \"format\":\"loom-mcp.eval.items\"}\n" + `{ "expected_tools":{"exact":false,"ordered":false,"tools":[{"args":{"a":1,"z":2},"name":"echo"}]}, "turns":["hello"],"id":"case","data":{"a":1,"z":9007199254740993}}` + "\n"
	file := eval.JSONL(fstest.MapFS{"items.jsonl": &fstest.MapFile{Data: []byte(text)}}, "items.jsonl")
	require.NoError(t, file.Err())
	assert.Equal(t, d.Hash(), file.Hash())
	changes := []func(*eval.Item){
		func(i *eval.Item) {
			i.ID = "other"
		}, func(i *eval.Item) {
			i.Turns = []string{"other"}
		},
		func(i *eval.Item) {
			i.Task = "task"
		}, func(i *eval.Item) {
			i.Reference = "reference"
		},
		func(i *eval.Item) {
			i.ExpectedOutcome = "outcome"
		}, func(i *eval.Item) {
			i.Data = json.RawMessage(`null`)
		},
		func(i *eval.Item) {
			i.ExpectedTools = nil
		}, func(i *eval.Item) {
			i.ExpectedTools.Ordered = true
		},
		func(i *eval.Item) {
			i.ExpectedTools.Tools[0].Name = "other"
		},
		func(i *eval.Item) {
			i.Turns = nil
			i.Input = "hello"
		},
		func(i *eval.Item) {
			i.ExpectedTools.Exact = true
		}, func(i *eval.Item) {
			i.ExpectedTools.Tools[0].Args = json.RawMessage(`{}`)
		},
	}
	for n, change := range changes {
		item := d.Items()[0]
		change(&item)
		changed := eval.Items(item)
		require.NoError(t, changed.Err(), n)
		assert.NotEqual(t, d.Hash(), changed.Hash(), n)
	}
}

func TestItemValidation(t *testing.T) {
	good := eval.Item{ID: "a-Z_1.2", Input: "hello"}
	invalidUTF8 := string([]byte{0xff})
	tests := []struct {
		name   string
		change func(*eval.Item)
	}{
		{"empty ID", func(i *eval.Item) {
			i.ID = ""
		}}, {"slash", func(i *eval.Item) {
			i.ID = "a/b"
		}},
		{"initial punctuation", func(i *eval.Item) {
			i.ID = "_a"
		}}, {"long ID", func(i *eval.Item) {
			i.ID = strings.Repeat("a", 129)
		}},
		{"no input", func(i *eval.Item) {
			i.Input = ""
		}}, {"both", func(i *eval.Item) {
			i.Turns = []string{"hi"}
		}},
		{"empty turn", func(i *eval.Item) {
			i.Input = ""
			i.Turns = []string{"hi", ""}
		}},
		{"persona", func(i *eval.Item) {
			i.Persona = "buyer"
		}}, {"single outcome", func(i *eval.Item) {
			i.ExpectedOutcome = "done"
		}},
		{"data", func(i *eval.Item) {
			i.Data = json.RawMessage(`{`)
		}},
		{"empty data", func(i *eval.Item) {
			i.Data = json.RawMessage{}
		}},
		{"empty tool args", func(i *eval.Item) {
			i.ExpectedTools = &expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: "x", Args: json.RawMessage{}}}}
		}},
		{"tool name", func(i *eval.Item) {
			i.ExpectedTools = &expect.ExpectedTools{Tools: []expect.ExpectedTool{{}}}
		}},
		{"tool args", func(i *eval.Item) {
			i.ExpectedTools = &expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: "x", Args: json.RawMessage(`{`)}}}
		}},
		{"UTF8 input", func(i *eval.Item) {
			i.Input = invalidUTF8
		}}, {"UTF8 turns", func(i *eval.Item) {
			i.Input = ""
			i.Turns = []string{invalidUTF8}
		}},
		{"UTF8 task", func(i *eval.Item) {
			i.Task = invalidUTF8
		}}, {"UTF8 reference", func(i *eval.Item) {
			i.Reference = invalidUTF8
		}},
		{"UTF8 outcome", func(i *eval.Item) {
			i.Input = ""
			i.Turns = []string{"hi"}
			i.ExpectedOutcome = invalidUTF8
		}},
		{"UTF8 tool", func(i *eval.Item) {
			i.ExpectedTools = &expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: tools.Ident(invalidUTF8)}}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item := good
			tc.change(&item)
			assert.Error(t, eval.Items(item).Err())
		})
	}
	assert.NoError(t, eval.Items(good).Err())
	assert.NoError(t, eval.Items(eval.Item{ID: strings.Repeat("a", 128), Turns: []string{"hi"}, ExpectedOutcome: "done", ExpectedTools: &expect.ExpectedTools{}}).Err())
	require.Error(t, eval.Items(good, good).Err())
	require.ErrorIs(t, eval.Items().Err(), eval.ErrEmptyDataset)
	require.ErrorIs(t, (eval.Dataset{}).Err(), eval.ErrEmptyDataset)
	missing := eval.JSONL(fstest.MapFS{}, "missing")
	require.ErrorIs(t, missing.Err(), fs.ErrNotExist)
	assert.Equal(t, "missing", missing.Source())
	var buf bytes.Buffer
	assert.ErrorIs(t, eval.EncodeJSONL(&buf, eval.Items()), eval.ErrEmptyDataset)
}

func TestDatasetRawJSONPresence(t *testing.T) {
	tests := []struct {
		name   string
		args   string
		data   string
		member string
	}{
		{name: "args null", args: `null`, member: `"args":null`},
		{name: "args object", args: `{}`, member: `"args":{}`},
		{name: "data object", data: `{}`, member: `"data":{}`},
		{name: "data null", data: `null`, member: `"data":null`},
		{name: "data string", data: `""`, member: `"data":""`},
		{name: "data array", data: `[]`, member: `"data":[]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			absent := eval.Item{ID: "one", Input: "hi", ExpectedTools: &expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: "echo.echo"}}}}
			present := eval.Items(absent).Items()[0]
			if tc.args != "" {
				present.ExpectedTools.Tools[0].Args = json.RawMessage(tc.args)
			}
			if tc.data != "" {
				present.Data = json.RawMessage(tc.data)
			}
			d := eval.Items(present)
			require.NoError(t, d.Err())
			assert.NotEqual(t, eval.Items(absent).Hash(), d.Hash())
			var encoded bytes.Buffer
			require.NoError(t, eval.EncodeJSONL(&encoded, d))
			assert.Contains(t, encoded.String(), tc.member)
			roundTrip, err := eval.DecodeJSONL(bytes.NewReader(encoded.Bytes()), "round trip")
			require.NoError(t, err)
			assert.Equal(t, d.Items(), roundTrip.Items())
			assert.Equal(t, d.Hash(), roundTrip.Hash())
			var absentEncoded bytes.Buffer
			require.NoError(t, eval.EncodeJSONL(&absentEncoded, eval.Items(absent)))
			assert.NotContains(t, absentEncoded.String(), `"args":`)
			assert.NotContains(t, absentEncoded.String(), `"data":`)
		})
	}
}
