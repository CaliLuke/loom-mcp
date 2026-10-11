package judge

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
)

// Cache stores completed calibrations, including label mismatches.
// Implementations must support concurrent use and return detached values.
type Cache interface {
	Load(key string) (*Calibration, bool)
	Store(*Calibration) error
}

// MemoryCache is a process-local, concurrency-safe calibration cache.
type MemoryCache struct {
	mu      sync.Mutex
	entries map[string]*Calibration
}

// DirCache persists completed calibrations with atomic file replacement.
// Missing, unreadable or corrupt files count as cache misses.
type DirCache struct{ dir string }

var (
	processCache    = NewMemoryCache()
	cacheKeyPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// NewMemoryCache returns an empty calibration cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{entries: make(map[string]*Calibration)}
}

// NewDirCache creates dir if needed and returns a file-backed calibration cache.
func NewDirCache(dir string) (*DirCache, error) {
	if dir == "" {
		return nil, errors.New("judge: cache directory is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("judge: create cache directory: %w", err)
	}
	return &DirCache{dir: dir}, nil
}

// Load returns a detached calibration, or a cache miss.
func (c *MemoryCache) Load(key string) (*Calibration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	return cloneCalibration(value), ok
}

// Store saves a detached copy of a completed calibration.
func (c *MemoryCache) Store(value *Calibration) error {
	if value == nil || !cacheKeyPattern.MatchString(value.Key) || value.Completed.IsZero() {
		return errors.New("judge: a completed calibration with a valid cache key is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[value.Key] = cloneCalibration(value)
	return nil
}

// Load reads a completed calibration; malformed content counts as a miss.
func (c *DirCache) Load(key string) (*Calibration, bool) {
	if !cacheKeyPattern.MatchString(key) {
		return nil, false
	}
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, false
	}
	var value Calibration
	if err := json.Unmarshal(data, &value, json.RejectUnknownMembers(true)); err != nil || value.Key != key || value.Completed.IsZero() {
		return nil, false
	}
	return &value, true
}

// Store atomically replaces the calibration file. Failed writes return an error.
func (c *DirCache) Store(value *Calibration) error {
	if value == nil || !cacheKeyPattern.MatchString(value.Key) || value.Completed.IsZero() {
		return errors.New("judge: a completed calibration with a valid cache key is required")
	}
	data, err := callrecord.Encode(value)
	if err != nil {
		return err
	}
	return writeAtomic(c.dir, c.path(value.Key), data)
}

func cloneCalibration(value *Calibration) *Calibration {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Examples = make([]ExampleResult, len(value.Examples))
	for i, result := range value.Examples {
		cloned.Examples[i] = result
		cloned.Examples[i].Want = maps.Clone(result.Want)
		cloned.Examples[i].Got = maps.Clone(result.Got)
	}
	return &cloned
}

func writeAtomic(dir, path string, data []byte) (err error) {
	file, err := os.CreateTemp(dir, ".calibration-*")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (c *DirCache) path(key string) string {
	return filepath.Join(c.dir, strings.TrimPrefix(key, "sha256:")+".json")
}
