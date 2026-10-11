package judge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestGolden(t *testing.T) {
	j, err := New(Config{Client: unusedClient{}, ModelID: "judge-model", MaxOutputTokens: 512})
	require.NoError(t, err)
	in := testInput()
	first, err := j.request(in)
	require.NoError(t, err)
	second, err := j.request(in)
	require.NoError(t, err)
	encoded, err := callrecord.Encode(first)
	require.NoError(t, err)
	repeated, err := callrecord.Encode(second)
	require.NoError(t, err)
	assert.Equal(t, encoded, repeated)
	golden(t, "request.json", encoded)
	schema, err := buildSchema(in.Claims)
	require.NoError(t, err)
	golden(t, "schema.json", schema)
	tool, err := callrecord.Encode(first.Tools[0])
	require.NoError(t, err)
	golden(t, "tool.json", tool)
	assert.Equal(t, "sha256:0ddf0971ec1f4aaea5242250db0bfdd8bb98b27947e9d4350ea33faede7c1ff4", j.PromptHash())
	assert.Equal(t, "judge-model", j.ModelID())
}

func TestRequestValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Input)
	}{
		{"no claims", func(in *Input) {
			in.Claims = nil
		}},
		{"bad ID", func(in *Input) {
			in.Claims[0].ID = "Bad"
		}},
		{"UTF8 ID", func(in *Input) {
			in.Claims[0].ID = "\xff"
		}},
		{"UTF8 claim", func(in *Input) {
			in.Claims[0].Text = "\xff"
		}},
		{"duplicate", func(in *Input) {
			in.Claims[1].ID = "a"
		}},
		{"empty claim", func(in *Input) {
			in.Claims[0].Text = ""
		}},
		{"subject", func(in *Input) {
			in.Subject = "bad-kind"
		}},
	}
	j, err := New(Config{Client: unusedClient{}, ModelID: "judge-model", MaxOutputTokens: 512})
	require.NoError(t, err)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := testInput()
			tt.edit(&in)
			_, err := j.request(in)
			require.ErrorIs(t, err, ErrInvalidInput)
		})
	}
}

func TestRequestLimitsAndFence(t *testing.T) {
	j, err := New(Config{Client: unusedClient{}, ModelID: "judge-model", MaxOutputTokens: 512, MaxInputBytes: 4096})
	require.NoError(t, err)
	in := testInput()
	in.Text = strings.Repeat("x", 4096)
	_, err = j.request(in)
	require.ErrorIs(t, err, ErrInputTooLarge)
	in = testInput()
	in.Text = "a ``` b ` c"
	in.Truncated = true
	req, err := j.request(in)
	require.NoError(t, err)
	encoded, err := callrecord.Encode(req)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "````\\na ``` b ` c\\n````")
	assert.Contains(t, string(encoded), "TRUNCATED")
	in.Claims = make([]Claim, 5000)
	for i := range in.Claims {
		in.Claims[i] = Claim{ID: fmt.Sprintf("claim_%04d", i), Text: "claim"}
	}
	_, err = j.request(in)
	require.ErrorIs(t, err, ErrSchemaTooLarge)
}

func TestLabels(t *testing.T) {
	for _, label := range []Label{Entailed, Contradicted, NotAddressed, Indeterminate} {
		assert.True(t, label.Valid())
	}
	assert.False(t, Label("unknown").Valid())
}

func testInput() Input {
	return Input{Subject: "answer", Text: "The sky is blue.", Task: "Describe the sky.", Reference: "It is daytime.", Claims: []Claim{{ID: "a", Text: "The sky is blue."}, {ID: "b", Text: "The sky is green."}}}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", filepath.Base(name)))
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(string(want)), string(got))
}

func TestSchemaPreservesClaimOrder(t *testing.T) {
	schema, err := buildSchema([]Claim{{ID: "b", Text: "B"}, {ID: "a", Text: "A"}})
	require.NoError(t, err)
	assert.Less(t, strings.Index(string(schema), `"b":`), strings.Index(string(schema), `"a":`))
	assert.Contains(t, string(schema), `"required":["b","a"]`)
}
