package suite

import (
	"bytes"
	stdjson "encoding/json"
	"encoding/json/v2"
	"errors"
	"example.com/longrun/env"
	"fmt"
	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
)

type inventoryAttempt struct {
	Key          string `json:"key"`
	Index        int    `json:"index"`
	Verdict      string `json:"verdict"`
	Variant      string `json:"variant"`
	Scenario     string `json:"scenario"`
	Item         string `json:"item"`
	Repeat       int    `json:"repeat"`
	HarnessError string `json:"harness_error"`
	Error        string `json:"error"`
	NotRun       string `json:"not_run"`
	StorageError string `json:"storage_error"`
}

func inspectInventoryExperiment(dir string, out *StoreInventory, id string, files map[string][]byte) error {
	headerBytes, ok := files[id+"/experiment.json"]
	if !ok {
		out.addEntry(id, "orphan", true, false)
		return nil
	}
	header, decodeErr := eval.DecodeExperimentHeader(headerBytes, id)
	if decodeErr != nil {
		out.addEntry(id, "invalid_header", true, false)
		return inventoryRecordedUnknown(out)
	}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasPrefix(path, id+"/") || path == id+"/experiment.json" || path == id+"/attempts.jsonl" {
			continue
		}
		kind := "unrecognized"
		if strings.HasPrefix(filepath.Base(path), ".experiment.json.tmp-") {
			kind = "temporary_header"
		}
		out.addEntry(path, kind, true, false)
	}
	if header.Status == "running" {
		out.addEntry(id, "running", true, false)
	} else if header.Status != "complete" {
		out.addEntry(id, "partial", true, false)
	}
	data, ok := files[id+"/attempts.jsonl"]
	if !ok {
		out.addEntry(id, "partial", true, false)
		return nil
	}
	lines := bytes.Split(data, []byte{'\n'})
	var first struct {
		Format     string `json:"format"`
		Version    int    `json:"version"`
		Experiment string `json:"experiment"`
	}
	if len(lines) == 0 || json.Unmarshal(lines[0], &first) != nil || first.Format != "loom-mcp.eval.attempts" || first.Version != 1 || first.Experiment != id {
		out.addEntry(id, "invalid_attempt_header", true, false)
		return inventoryRecordedUnknown(out)
	}
	heldout, err := inventoryHeldout(dir, out.Configuration)
	if err != nil {
		return err
	}
	seen := map[eval.AttemptKey]bool{}
	count := 0
	for index, line := range lines[1:] {
		if len(bytes.TrimSpace(line)) == 0 && index == len(lines)-2 {
			continue
		}
		var a inventoryAttempt
		if json.Unmarshal(line, &a) != nil || a.Variant == "" || a.Scenario == "" || a.Item == "" || a.Repeat < 1 {
			out.addEntry(id, "partial_attempt", true, false)
			continue
		}
		key := eval.AttemptKey{Variant: a.Variant, Scenario: a.Scenario, Item: a.Item, Repeat: a.Repeat}
		if a.Key != key.String() || a.Index < 0 || !slices.Contains([]string{"passed", "failed", "error", "pending", "not_run"}, a.Verdict) {
			out.addEntry(id, "partial_attempt", true, false)
		}
		if a.Verdict == "error" || a.Verdict == "pending" || a.Verdict == "not_run" {
			out.addEntry(id, "partial_attempt", true, false)
		}
		if seen[key] {
			out.addEntry(id, "duplicate_attempt", true, false)
		}
		seen[key] = true
		count++
		if a.HarnessError != "" || a.Error != "" || a.NotRun != "" || a.StorageError != "" {
			out.addEntry(id, "partial_attempt", true, false)
		}
		config, known := inventoryConfiguration(header.Label)
		if !known || len(heldout) == 0 {
			out.addEntry(id, "unknown_consumption", true, false)
		}
		if known && config == out.Configuration && config != "offline" && a.Variant != "baseline" && heldout[a.Scenario+"/"+a.Item] {
			out.addEntry(id, "heldout_variant", false, true)
		}
	}
	if header.Planned < 1 || count != header.Planned {
		out.addEntry(id, "partial", true, false)
	} else if header.Status == "complete" {
		out.addEntry(id, "complete", false, false)
	}
	return nil
}

func inventoryConfiguration(label string) (string, bool) {
	parts := strings.Split(label, "/")
	if len(parts) >= 2 && (parts[0] == "gate" || parts[0] == "pilot") {
		switch parts[1] {
		case "offline", "anthropic", "bedrock-anthropic", "openai", "gemini":
			return parts[1], true
		}
	}
	return "", false
}

func inventoryHeldout(dir, configuration string) (map[string]bool, error) {
	frozen, err := inventoryFrozenPools(dir, configuration)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, scenario := range env.ScenarioIDs(configuration) {
		for _, part := range []struct {
			name  string
			count int
		}{{"look1", 12}, {"look2", 12}, {"look3", 24}} {
			pool, err := LoadPool(dir, scenario, part.name)
			if err != nil {
				return nil, fmt.Errorf("inventory complete heldout manifest: %w", err)
			}
			items := pool.Items()
			if len(items) != part.count {
				return nil, fmt.Errorf("inventory heldout count mismatch: %s/%s", scenario, part.name)
			}
			if frozen != nil {
				key := scenario + "/" + part.name
				want, ok := frozen[key]
				if !ok || len(want.Items) != part.count {
					return nil, fmt.Errorf("inventory frozen heldout manifest missing %s", key)
				}
				raw, err := readFile(dir, "testdata/pools/"+key+".jsonl")
				if err != nil {
					return nil, err
				}
				if gate.Digest(raw) != want.DatasetHash {
					return nil, fmt.Errorf("inventory frozen heldout bytes changed: %s", key)
				}
				for i, item := range items {
					if item.ID != want.Items[i].ID {
						return nil, fmt.Errorf("inventory frozen heldout identity changed: %s", key)
					}
				}
			}
			for _, item := range items {
				key := scenario + "/" + item.ID
				if out[key] {
					return nil, fmt.Errorf("inventory duplicate heldout item %s", key)
				}
				out[key] = true
			}
		}
	}
	return out, nil
}

func inventoryFrozenPools(dir, configuration string) (map[string]gate.Pool, error) {
	paths := []string{freezePath(dir, configuration)}
	if configuration != "offline" {
		paths = append(paths, freezePath(dir, "offline"))
	}
	for _, path := range paths {
		data, err := readFile(filepath.Dir(path), filepath.Base(path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var f gate.Freeze
		if err := stdjson.Unmarshal(data, &f); err != nil {
			return nil, err
		}
		hash, err := f.Hash()
		if err != nil {
			return nil, err
		}
		expected, err := os.ReadFile(strings.TrimSuffix(path, ".json") + ".sha256")
		if err != nil || hash != strings.TrimSpace(string(expected)) || f.Format != gate.FreezeFormat || (f.Version != 1 && f.Version != 2) {
			return nil, fmt.Errorf("inventory requires an intact frozen pool manifest: %s", path)
		}
		out := map[string]gate.Pool{}
		for _, pool := range f.Pools {
			key := pool.Scenario + "/" + pool.Partition
			if _, exists := out[key]; exists {
				return nil, fmt.Errorf("inventory duplicate frozen pool %s", key)
			}
			out[key] = pool
		}
		return out, nil
	}
	return nil, nil
}

// A malformed entry is evidence, not a failed inventory scan. Completion remains
// fail-closed through Unknown; never lose that marker while collecting entries.
func inventoryRecordedUnknown(out *StoreInventory) error {
	if !out.Unknown {
		return fmt.Errorf("invalid inventory entry lost its unknown marker")
	}
	return nil
}
