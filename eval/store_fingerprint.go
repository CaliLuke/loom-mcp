package eval

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type fingerprintContext struct {
	ContextSettings    *compaction.ContextSettings `json:"context_settings"`
	RecoverySettings   *run.ContextRecovery        `json:"recovery_settings"`
	CompactionSettings *compaction.Settings        `json:"compaction_settings"`
	KeepResultTools    []tools.Ident               `json:"keep_result_tools"`
}

// recordFingerprint reads all call and settings identities. The legacy Context
// field continues to hash root context settings for store compatibility.
func recordFingerprint(rec *runrecord.RunRecord) (Fingerprint, error) {
	f := Fingerprint{Models: make(map[model.ModelClass][]string)}
	for _, r := range rec.Runs {
		if r.ContextFingerprint != "" {
			f.ContextFingerprints = append(f.ContextFingerprints, r.ContextFingerprint)
		}
		if r.RunID == rec.RootRunID {
			data, err := callrecord.Encode(fingerprintContext{r.ContextSettings, r.RecoverySettings, r.CompactionSettings, r.KeepResultTools})
			if err != nil {
				return Fingerprint{}, err
			}
			f.Context = append(f.Context, fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
		}
		for _, c := range r.Calls {
			f.Models[c.Started.Class] = append(f.Models[c.Started.Class], c.Started.ModelID)
			f.Prompts = append(f.Prompts, c.Started.Prompts...)
			if c.Started.Request.System != "" {
				f.SystemHashes = append(f.SystemHashes, string(c.Started.Request.System))
			}
			if c.Started.Request.Tools != "" {
				f.ToolHashes = append(f.ToolHashes, string(c.Started.Request.Tools))
			}
		}
	}
	return mergeFingerprint(Fingerprint{}, f), nil
}

// mergeFingerprint returns an owned, sorted set union for deterministic summaries.
func mergeFingerprint(a, b Fingerprint) Fingerprint {
	out := Fingerprint{Models: make(map[model.ModelClass][]string)}
	for k, v := range a.Models {
		out.Models[k] = slices.Clone(v)
	}
	for k, v := range b.Models {
		out.Models[k] = append(out.Models[k], v...)
	}
	for k, v := range out.Models {
		out.Models[k] = sortedUnique(v)
	}
	out.Prompts = append(slices.Clone(a.Prompts), b.Prompts...)
	slices.SortFunc(out.Prompts, func(a, b prompt.PromptRef) int {
		if order := cmp.Compare(a.ID, b.ID); order != 0 {
			return order
		}
		return cmp.Compare(a.Version, b.Version)
	})
	out.Prompts = slices.Compact(out.Prompts)
	out.SystemHashes = sortedUnique(append(slices.Clone(a.SystemHashes), b.SystemHashes...))
	out.ToolHashes = sortedUnique(append(slices.Clone(a.ToolHashes), b.ToolHashes...))
	out.ContextFingerprints = sortedUnique(append(slices.Clone(a.ContextFingerprints), b.ContextFingerprints...))
	out.Context = sortedUnique(append(slices.Clone(a.Context), b.Context...))
	return out
}

func sortedUnique(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}
