package eval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type strictObject struct {
	name   string
	prefix string
	member string
}

// AC5/F1: each mutation changes one member in an otherwise valid document.
func TestDecodeAttemptStrict(t *testing.T) {
	data, err := readStoreTestFile("testdata/store/attempt_judged.golden.jsonl")
	require.NoError(t, err)
	valid := string(data)
	valid = strings.Replace(valid, `"runs":[]`, `"runs":[{"run_id":"r","export":{"version":1,"root_run_id":"r","runs":[],"content":{}}}]`, 1)
	valid = strings.Replace(valid, `"prompts":[]`, `"prompts":[{"id":"system","version":"1"}]`, 1)
	decode := func(data []byte) error {
		_, err := decodeAttemptLine(data)
		return err
	}
	testStrictObjects(t, valid, decode, []strictObject{
		{"attempt", "{", `"index":0`},
		{"check", `"checks":[{`, `"name":"ok"`},
		{"score", `"scores":[{`, `"name":"quality"`},
		{"claim", `"claims":[{`, `"id":"helpful"`},
		{"judgment", `"judgments":[{`, `"claim_id":"helpful"`},
		{"step", `"step":{`, `"run":0`},
		{"judge_usage", `"judge_usage":{`, `"TotalTokens":7`},
		{"fingerprint", `"fingerprint":{`, `"models":{}`},
		{"prompt", `"prompts":[{`, `"id":"system"`},
		{"run", `"runs":[{`, `"run_id":"r"`},
		{"export", `"export":{`, `"version":1`},
	})
	t.Run("export_version", func(t *testing.T) {
		require.NoError(t, decode([]byte(valid)))
		mutated := strings.Replace(valid, `"version":1`, `"version":2`, 1)
		err := decode([]byte(mutated))
		require.ErrorContains(t, err, "runs[0].export")
		require.ErrorContains(t, err, "unsupported export version")
	})
}

func TestDecodeExperimentStrict(t *testing.T) {
	data, err := readStoreTestFile("testdata/store/experiment.golden.json")
	require.NoError(t, err)
	valid := string(data)
	decode := func(data []byte) error {
		_, err := decodeExperiment(data, "0199d77a-0000-7000-8000-000000000001")
		return err
	}
	testStrictObjects(t, valid, decode, []strictObject{
		{"experiment", "{", `"version":1`},
		{"config", `"config":{`, `"repeats":1`},
		{"selection", `"selection":{`, `"variants":[]`},
		{"scenario", `"scenarios":[{`, `"id":"chat"`},
		{"source", `"source":{`, `"system":"git"`},
		{"versions", `"versions":{`, `"go":"go1.27.2"`},
		{"variant", `"variants":[{`, `"id":"default"`},
	})
}

// testStrictObjects first proves the baseline decodes, then inserts exactly one
// unknown or repeated member. Repeated values match so lenient decoding stays valid.
func testStrictObjects(t *testing.T, valid string, decode func([]byte) error, objects []strictObject) {
	t.Helper()
	require.NoError(t, decode([]byte(valid)))
	for _, object := range objects {
		require.Contains(t, valid, object.prefix)
		require.Contains(t, valid, object.member)
		escaped := fmt.Sprintf(`"\u%04x%s`, object.member[1], object.member[2:])
		for _, mutation := range []struct{ name, member, want string }{
			{"unknown", `"unexpected":true`, "unknown object member name"},
			{"duplicate", object.member, "duplicate object member name"},
			{"escaped_duplicate", escaped, "duplicate object member name"},
		} {
			t.Run(object.name+"/"+mutation.name, func(t *testing.T) {
				mutated := strings.Replace(valid, object.prefix, object.prefix+mutation.member+",", 1)
				require.ErrorContains(t, decode([]byte(mutated)), mutation.want)
			})
		}
	}
}
