package env_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/longrun/env"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/stretchr/testify/require"
)

func TestK19cImmutablePools(t *testing.T) {
	root, err := os.OpenRoot("../testdata/pools")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	archives, err := filepath.Glob("../gate/archive/v1/offline/*/freeze_default.json")
	require.NoError(t, err)
	require.Len(t, archives, 1)
	archiveRoot, err := os.OpenRoot(filepath.Dir(archives[0]))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, archiveRoot.Close()) })
	data, err := archiveRoot.ReadFile("freeze_default.json")
	require.NoError(t, err)
	var frozen gate.Freeze
	require.NoError(t, json.Unmarshal(data, &frozen))
	hashes := map[string]string{}
	for _, p := range frozen.Pools {
		hashes[p.Scenario+"/"+p.Partition] = p.DatasetHash
	}
	paths, err := filepath.Glob("../testdata/pools/*/*.jsonl")
	require.NoError(t, err)
	require.Len(t, paths, 84)
	generated := map[uint64]map[string][]eval.Item{}
	for _, seed := range []uint64{20261020, 20261021, 20261022} {
		generated[seed], err = env.Generate(seed)
		require.NoError(t, err)
	}
	for _, path := range paths {
		key := filepath.Base(filepath.Dir(path)) + "/" + strings.TrimSuffix(filepath.Base(path), ".jsonl")
		seed := uint64(20261020)
		if strings.HasPrefix(key, "large_result_middle/") {
			seed = 20261021
		}
		if strings.HasPrefix(key, "recall_pressure/") {
			seed = 20261022
		}
		var want bytes.Buffer
		require.NoError(t, eval.EncodeJSONL(&want, eval.Items(generated[seed][key]...)))
		actual, err := root.ReadFile(key + ".jsonl")
		require.NoError(t, err)
		require.NoError(t, checkPoolBytes(actual, want.Bytes(), hashes[key]), key)
		t.Run(key, func(t *testing.T) {
			require.Error(t, checkPoolBytes(actual, want.Bytes(), "flipped"))
			edited := bytes.Replace(actual, []byte("Audit"), []byte("Alter"), 1)
			if bytes.Equal(edited, actual) {
				edited = append(append([]byte(nil), actual...), '\n')
			}
			require.Error(t, checkPoolBytes(edited, want.Bytes(), hashes[key]))
		})
	}
}

func checkPoolBytes(actual, want []byte, hash string) error {
	if !bytes.Equal(actual, want) {
		return fmt.Errorf("committed pool differs from Generate(seed)")
	}
	if gate.Digest(actual) != hash {
		return fmt.Errorf("dataset_hash differs from generated pool")
	}
	return nil
}
