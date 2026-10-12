package summaryeval

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	agRuntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

type cachedSide struct {
	Schema string `json:"schema"`
	Key    string `json:"key"`
	Side   Side   `json:"side"`
}

func cachePath(dir string, c Cut, in *agRuntime.SummarizeContextInput, repeat int) (string, string, error) {
	cp, _ := findCheckpoint(c.run, c.Record)
	key, err := encode(struct {
		Schema, Root, Run                                   string
		Step                                                int
		Record                                              any
		Chain                                               any
		Template                                            any
		Instructions                                        string
		Model                                               any
		MaxSummaryChunks, MaxSummarisedResultTokens, Repeat int
	}{cacheSchema, c.record.RootRunID, c.RunID, c.Step, c.Record, cp.Chain, in.ContextSettings.Prompt.Template, in.Settings.Instructions, in.ContextModel, in.ContextSettings.MaxSummaryChunks, in.ContextSettings.MaxSummarisedResultTokens, repeat})
	if err != nil {
		return "", "", err
	}
	hash := strings.TrimPrefix(string(contentHash(key)), "sha256:")
	return filepath.Join(dir, "_compaction", "cache", hash+".json"), hash, nil
}

func cached(path, key string) (Side, bool) {
	f, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return Side{}, false
	}
	b, readErr := io.ReadAll(f)
	err = errors.Join(readErr, f.Close())
	if err != nil {
		return Side{}, false
	}
	var entry cachedSide
	if decode(b, &entry) != nil || entry.Schema != cacheSchema || entry.Key != key {
		return Side{}, false
	}
	entry.Side.Cached = true
	return entry.Side, true
}
