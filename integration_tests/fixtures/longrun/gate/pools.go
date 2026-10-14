// Package suite owns the longrun gate's filesystem and experiment boundaries.
package suite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"example.com/longrun/env"
	"example.com/longrun/scenarios"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// WriteItems generates every partition before any freeze; it never edits individual items.
func WriteItems(dir string, seed uint64) error {
	freezes, err := filepath.Glob(filepath.Join(dir, "gate", "freeze_*.json"))
	if err != nil {
		return err
	}
	archived, err := filepath.Glob(filepath.Join(dir, "gate/archive/v1/*/*/freeze_*.json"))
	if err != nil {
		return err
	}
	var frozen *gate.Freeze
	if len(freezes)+len(archived) > 0 {
		frozen = &gate.Freeze{}
	}
	if err := gate.CanGenerateItems(frozen); err != nil {
		return err
	}
	pools, err := env.Generate(seed)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(pools))
	for key := range pools {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		var data bytes.Buffer
		if err := eval.EncodeJSONL(&data, eval.Items(pools[key]...)); err != nil {
			return err
		}
		path := filepath.Join(dir, "testdata", "pools", key+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return err
		}
		if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
			return err
		}
	}
	return nil
}

// LoadPool eagerly validates one partition.
func LoadPool(dir, scenario, partition string) (eval.Dataset, error) {
	d := eval.JSONL(os.DirFS(filepath.Join(dir, "testdata", "pools")), scenario+"/"+partition+".jsonl")
	return d, d.Err()
}

// Scenarios builds the runtime suite for one held-out partition.
func Scenarios(dir, config, partition string) ([]eval.Scenario, error) {
	var out []eval.Scenario
	for _, id := range env.ScenarioIDs(config) {
		if id == "model_change" && partition == "pilot" {
			continue
		}
		d, err := LoadPool(dir, id, partition)
		if err != nil {
			return nil, err
		}
		out = append(out, scenarios.New(id, d))
	}
	return out, nil
}

// Manifest supplies every planned cumulative item, even when all its runs are missing.
func Manifest(dir, config string, look int) (map[string][]string, error) {
	if look < 1 || look > 3 {
		return nil, fmt.Errorf("invalid look %d", look)
	}
	out := map[string][]string{}
	for _, id := range env.GatedScenarios(config) {
		for n := 1; n <= look; n++ {
			d, err := LoadPool(dir, id, fmt.Sprintf("look%d", n))
			if err != nil {
				return nil, err
			}
			for _, item := range d.Items() {
				out[id] = append(out[id], item.ID)
			}
		}
	}
	return out, nil
}

// Inputs reads raw pool bytes and independently rematerialises every frozen item.
func Inputs(dir string) (gate.FreezeInputs, []gate.Pool, error) {
	in := gate.FreezeInputs{Pools: map[string][]byte{}, Materialized: map[string][]byte{}, MaterializerVersion: env.MaterializerVersion}
	paths, err := filepath.Glob(filepath.Join(dir, "testdata/pools/*/*.jsonl"))
	if err != nil {
		return in, nil, err
	}
	if len(paths) == 0 {
		return in, nil, fmt.Errorf("no pools; run items first")
	}
	var pools []gate.Pool
	for _, path := range paths {
		scenario := filepath.Base(filepath.Dir(path))
		partition := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return in, nil, err
		}
		raw, err := readFile(dir, relative)
		if err != nil {
			return in, nil, err
		}
		in.Pools[scenario+"/"+partition] = raw
		d, err := eval.DecodeJSONL(bytes.NewReader(raw), path)
		if err != nil {
			return in, nil, err
		}
		p := gate.Pool{Scenario: scenario, Partition: partition, DatasetHash: gate.Digest(raw)}
		for _, item := range d.Items() {
			m, err := env.Materialize(item)
			if err != nil {
				return in, nil, err
			}
			data, err := json.Marshal(m)
			if err != nil {
				return in, nil, err
			}
			if _, exists := in.Materialized[item.ID]; exists {
				return in, nil, fmt.Errorf("duplicate item %s", item.ID)
			}
			in.Materialized[item.ID] = data
			p.Items = append(p.Items, gate.FrozenItem{ID: item.ID, SHA256: gate.Digest(data)})
		}
		pools = append(pools, p)
	}
	return in, pools, nil
}
func writeOnce(path string, data []byte) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	f, err := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Close())
}

// readFile confines externally selected filenames to the fixture directory.
func readFile(dir, name string) ([]byte, error) {
	f, err := os.OpenInRoot(dir, name)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(f)
	return data, errors.Join(readErr, f.Close())
}
