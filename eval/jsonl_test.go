package eval_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const header = `{"format":"loom-mcp.eval.items","version":1}`
const row = `{"id":"one","input":"hi"}`

func TestJSONLStrictDecode(t *testing.T) {
	tests := []struct {
		name, text string
		line       int
	}{
		{"missing header", row, 1}, {"empty", "", 1}, {"format", `{"format":"other","version":1}`, 1},
		{"version", `{"format":"loom-mcp.eval.items","version":2}`, 1}, {"missing version", `{"format":"loom-mcp.eval.items"}`, 1},
		{"unknown header", `{"format":"loom-mcp.eval.items","version":1,"extra":0}`, 1},
		{"repeated header", header + "\n" + header, 2}, {"header array", `[]`, 1}, {"null header", `null`, 1},
		{"blank", header + "\n\n" + row, 2}, {"extra newline", header + "\n" + row + "\n\n", 3},
		{"two values", header + "\n" + row + " {}", 2}, {"array", header + "\n[]", 2}, {"null", header + "\nnull", 2},
		{"unknown item", header + "\n" + `{"id":"x","input":"hi","extra":0}`, 2},
		{"unknown tools", header + "\n" + `{"id":"x","input":"hi","expected_tools":{"tools":[],"extra":0}}`, 2},
		{"unknown tool", header + "\n" + `{"id":"x","input":"hi","expected_tools":{"tools":[{"name":"x","extra":0}]}}`, 2},
		{"duplicate header member", `{"format":"loom-mcp.eval.items","version":1,"version":1}`, 1},
		{"duplicate tools member", header + "\n" + `{"id":"x","input":"hi","expected_tools":{"tools":[],"tools":[]}}`, 2},
		{"duplicate", header + "\n" + `{"id":"x","id":"y","input":"hi"}`, 2},
		{"escaped duplicate", header + "\n" + `{"id":"x","\u0069d":"y","input":"hi"}`, 2},
		{"data duplicate", header + "\n" + `{"id":"x","input":"hi","data":{"n":1,"n":2}}`, 2},
		{"args duplicate", header + "\n" + `{"id":"x","input":"hi","expected_tools":{"tools":[{"name":"x","args":{"n":1,"n":2}}]}}`, 2},
		{"UTF8", header + "\n" + "{\"id\":\"x\",\"input\":\"\xff\"}", 2},
		{"invalid item", header + "\n" + `{"id":"x"}`, 2},
		{"duplicate ID", header + "\n" + row + "\n" + row, 3},
		{"too large", header + "\n" + `{"id":"x","input":"` + strings.Repeat("a", 16<<20) + `"}`, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eval.DecodeJSONL(strings.NewReader(tc.text), "source")
			var de *eval.DecodeError
			require.ErrorAs(t, err, &de)
			assert.Equal(t, tc.line, de.Line)
			assert.Equal(t, "source", de.Source)
			assert.Error(t, errors.Unwrap(de))
		})
	}
	d, err := eval.DecodeJSONL(strings.NewReader(header+"\n"+row), "no final LF")
	require.NoError(t, err)
	assert.Equal(t, 1, d.Len())
}

func FuzzDecodeJSONL(f *testing.F) {
	f.Add(header + "\n" + row + "\n")
	f.Add(header + "\n" + `{"id":"n","input":"hi","data":{"n":9007199254740993}}` + "\n")
	f.Add(header + "\n" + `{"id":"n","input":"hi","expected_tools":{"tools":[{"name":"echo.echo","args":null}]}}` + "\n")
	f.Add(header + "\n" + `{"id":"n","input":"hi","expected_tools":{"tools":[{"name":"echo.echo","args":{}}]}}` + "\n")
	f.Add(header + "\n" + `{"id":"n","input":"hi","data":{}}` + "\n")
	f.Add(header + "\n" + `{"id":"n","input":"hi","data":null}` + "\n")
	f.Add(header + "\n" + `{"id":"n","input":"hi","data":""}` + "\n")
	f.Add(header + "\n" + `{"id":"n","input":"hi","data":[]}` + "\n")
	f.Add("")
	f.Add(header + "\nnull\n")
	f.Fuzz(func(t *testing.T, input string) {
		d, err := eval.DecodeJSONL(strings.NewReader(input), "fuzz")
		if err != nil {
			return
		}
		var first bytes.Buffer
		require.NoError(t, eval.EncodeJSONL(&first, d))
		second, err := eval.DecodeJSONL(bytes.NewReader(first.Bytes()), "round trip")
		require.NoError(t, err)
		var next bytes.Buffer
		require.NoError(t, eval.EncodeJSONL(&next, second))
		third, err := eval.DecodeJSONL(bytes.NewReader(next.Bytes()), "canonical round trip")
		require.NoError(t, err)
		assert.Equal(t, second.Items(), third.Items())
		assert.Equal(t, first.String(), next.String())
		assert.Equal(t, d.Hash(), second.Hash())
	})
}
