package suite

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
	"github.com/google/uuid"
)

// InventoryFile binds every inspected regular file to its exact bytes.
type InventoryFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// InventoryEntry classifies a persisted store entry, including unpublished ones.
type InventoryEntry struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Unknown  bool   `json:"unknown"`
	Consumed bool   `json:"consumed"`
}

// StoreInventory records the explicitly declared historical store boundary.
// It cannot discover experiments in undeclared external stores.
type StoreInventory struct {
	Version          int              `json:"version"`
	Configuration    string           `json:"configuration"`
	Store            string           `json:"store"`
	DeclaredComplete bool             `json:"declared_complete"`
	Absent           bool             `json:"absent"`
	Files            []InventoryFile  `json:"files"`
	Entries          []InventoryEntry `json:"entries"`
	Unknown          bool             `json:"unknown"`
	Consumed         bool             `json:"consumed"`
	Limitation       string           `json:"limitation"`
	SHA256           string           `json:"sha256"`
}

// InventoryStore inspects the named store without creating it or hiding staged,
// malformed or interrupted entries. A declaration is mandatory.
func InventoryStore(dir, configuration, storePath string, declaredComplete bool) (out StoreInventory, err error) {
	if !declaredComplete || storePath == "" || configuration == "" {
		return out, fmt.Errorf("inventory requires an explicit named store and complete-store declaration")
	}
	absolute, err := filepath.Abs(storePath)
	if err != nil {
		return out, err
	}
	out = StoreInventory{Version: 1, Configuration: configuration, Store: absolute, DeclaredComplete: true, Limitation: "This declaration covers only the named store; undeclared external stores cannot be discovered. A fresh worktree alone is not historical inventory."}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		out.Absent = true
		return finishInventory(out)
	}
	if err != nil {
		return out, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		out.addEntry(".", "symlink_or_non_directory", true, false)
		return finishInventory(out)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	files := map[string][]byte{}
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			out.addEntry(path, "symlink", true, false)
			return nil
		}
		if entry.IsDir() {
			if strings.Contains(path, "/") {
				out.addEntry(path, "reserved_directory", true, false)
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			out.addEntry(path, "reserved", true, false)
			return nil
		}
		data, readErr := root.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[path] = data
		out.Files = append(out.Files, InventoryFile{Path: path, SHA256: gate.Digest(data)})
		return nil
	})
	if err != nil {
		return out, err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		switch {
		case name == ".gitignore" && !entry.IsDir():
		case strings.HasPrefix(name, ".experiment.tmp-"):
			out.addEntry(name, "staging", true, false)
		case strings.HasPrefix(name, ".experiment.json.tmp-"):
			out.addEntry(name, "temporary_header", true, false)
		case entry.IsDir():
			if _, parseErr := uuid.Parse(name); parseErr != nil {
				out.addEntry(name, "unrecognized", true, false)
				continue
			}
			if err := inspectInventoryExperiment(dir, &out, name, files); err != nil {
				return out, err
			}
		default:
			out.addEntry(name, "unrecognized", true, false)
		}
	}
	return finishInventory(out)
}

func finishInventory(out StoreInventory) (StoreInventory, error) {
	out.SHA256 = ""
	data, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	out.SHA256 = gate.Digest(data)
	return out, nil
}

func (out *StoreInventory) addEntry(path, kind string, unknown, consumed bool) {
	out.Entries = append(out.Entries, InventoryEntry{Path: path, Kind: kind, Unknown: unknown, Consumed: consumed})
	out.Unknown = out.Unknown || unknown
	out.Consumed = out.Consumed || consumed
}
