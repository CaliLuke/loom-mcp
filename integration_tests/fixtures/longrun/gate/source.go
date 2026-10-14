package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// SourceFile binds one compiler-selected local source or dependency-owner file.
type SourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

// SourceModule records the selected graph, including versioned replacements.
// Local is a logical repository-relative module root, never a checkout path.
type SourceModule struct {
	Path     string        `json:"path"`
	Version  string        `json:"version"`
	Sum      string        `json:"sum"`
	GoModSum string        `json:"go_mod_sum"`
	Local    string        `json:"local,omitempty"`
	Replace  *SourceModule `json:"replace,omitempty"`
}

// SourceIdentity is independent of checkout location, HEAD and artifact bytes.
// Those belong to recovery provenance, not executable semantic equality.
type SourceIdentity struct {
	Version   int               `json:"version"`
	Owners    []SourceFile      `json:"owners"`
	Modules   []SourceModule    `json:"modules"`
	Build     map[string]string `json:"build"`
	Toolchain map[string]string `json:"toolchain"`
	SHA256    string            `json:"sha256"`
}

// SourceSnapshot resolves the command and design dependency closure against the
// selected workspace and hashes its source, embedded assets and module inputs.
func SourceSnapshot(ctx context.Context, root string) (SourceIdentity, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return SourceIdentity{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return SourceIdentity{}, err
	}
	dir := filepath.Join(root, "integration_tests/fixtures/longrun")
	build, err := sourceEnvironment(ctx, dir, root)
	if err != nil {
		return SourceIdentity{}, err
	}
	identity := SourceIdentity{Version: 1, Build: build, Toolchain: make(map[string]string)}
	if _, err := sourceGo(ctx, dir, "mod", "verify"); err != nil {
		return identity, err
	}
	owners := make(map[string]bool)
	owners["go.work"] = true
	moduleData, err := sourceGo(ctx, dir, "list", "-mod=readonly", "-m", "-json", "all")
	if err != nil {
		return identity, err
	}
	var modules []listedSourceModule
	if err := decodeSourceStream(moduleData, &modules); err != nil {
		return identity, err
	}
	cacheData, err := sourceGo(ctx, dir, "env", "GOMODCACHE")
	if err != nil {
		return identity, err
	}
	cache := strings.TrimSpace(string(cacheData))
	for _, module := range modules {
		resolved, err := sourceModuleIdentity(root, cache, module)
		if err != nil {
			return identity, err
		}
		identity.Modules = append(identity.Modules, resolved)
		if module.Main {
			local, err := sourceRelative(root, module.Dir)
			if err != nil {
				return identity, err
			}
			owners[filepath.ToSlash(filepath.Join(local, "go.mod"))] = true
			owners[filepath.ToSlash(filepath.Join(local, "go.sum"))] = true
		}
	}
	packageData, err := sourceGo(ctx, dir, "list", "-mod=readonly", "-deps", "-json", "./cmd/compaction-gate", "./design")
	if err != nil {
		return identity, err
	}
	var packages []listedSourcePackage
	if err := decodeSourceStream(packageData, &packages); err != nil {
		return identity, err
	}
	goRootData, err := sourceGo(ctx, dir, "env", "GOROOT")
	if err != nil {
		return identity, err
	}
	goRoot := strings.TrimSpace(string(goRootData))
	usedModules := make(map[string]bool)
	for _, pkg := range packages {
		if pkg.Error != nil {
			return identity, fmt.Errorf("source package %s: %s", pkg.ImportPath, pkg.Error.Err)
		}
		if pkg.Standard {
			for _, file := range pkg.files() {
				path := filepath.Join(pkg.Dir, file)
				relative, err := sourceRelative(goRoot, path)
				if err != nil {
					return identity, err
				}
				data, err := readFile(goRoot, relative)
				if err != nil {
					return identity, err
				}
				identity.Toolchain[relative] = gate.Digest(data)
			}
			continue
		}
		if pkg.Module == nil {
			return identity, fmt.Errorf("source package %s has no module identity", pkg.ImportPath)
		}
		local := pkg.Module.Main || (pkg.Module.Replace != nil && pkg.Module.Replace.Version == "")
		if !local {
			resolved := pkg.Module
			if resolved.Replace != nil {
				resolved = resolved.Replace
			}
			usedModules[resolved.Path+"@"+resolved.Version] = true
			continue
		}
		for _, file := range pkg.files() {
			relative, err := sourceRelative(root, filepath.Join(pkg.Dir, file))
			if err != nil {
				return identity, err
			}
			owners[relative] = true
		}
	}
	for i := range identity.Modules {
		if err := selectedSourceSums(cache, &identity.Modules[i], usedModules); err != nil {
			return identity, err
		}
	}
	for path := range owners {
		data, err := readFile(root, path)
		if err != nil {
			return identity, fmt.Errorf("source owner %s: %w", path, err)
		}
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			return identity, err
		}
		mode := uint32(0644)
		if info.Mode().Perm()&0111 != 0 {
			mode = 0755
		}
		identity.Owners = append(identity.Owners, SourceFile{Path: path, SHA256: gate.Digest(data), Mode: mode})
	}
	for _, tool := range []string{"bin/go", "pkg/tool/" + build["GOHOSTOS"] + "_" + build["GOHOSTARCH"] + "/compile", "pkg/tool/" + build["GOHOSTOS"] + "_" + build["GOHOSTARCH"] + "/link", "pkg/tool/" + build["GOHOSTOS"] + "_" + build["GOHOSTARCH"] + "/asm"} {
		data, err := readFile(goRoot, tool)
		if err != nil {
			return identity, err
		}
		identity.Toolchain[tool] = gate.Digest(data)
	}
	// Assembly also consumes shared headers outside package-local file lists.
	// Hash their actual bytes, not the GOROOT location or modification times.
	headers, err := os.ReadDir(filepath.Join(goRoot, "pkg/include"))
	if err != nil {
		return identity, err
	}
	if len(headers) == 0 {
		return identity, fmt.Errorf("source toolchain has no shared assembler headers")
	}
	for _, header := range headers {
		if !header.Type().IsRegular() {
			return identity, fmt.Errorf("unsupported shared assembler input %s", header.Name())
		}
		path := filepath.ToSlash(filepath.Join("pkg/include", header.Name()))
		data, err := readFile(goRoot, path)
		if err != nil {
			return identity, err
		}
		identity.Toolchain[path] = gate.Digest(data)
	}
	sort.Slice(identity.Owners, func(i, j int) bool { return identity.Owners[i].Path < identity.Owners[j].Path })
	sort.Slice(identity.Modules, func(i, j int) bool { return identity.Modules[i].Path < identity.Modules[j].Path })
	data, err := json.Marshal(identity)
	if err != nil {
		return identity, err
	}
	identity.SHA256 = gate.Digest(data)
	return identity, nil
}

func sourceRelative(root, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("source input outside logical root: %s", path)
	}
	return filepath.ToSlash(relative), nil
}

func decodeSourceStream[T any](data []byte, out *[]T) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var value T
		err := decoder.Decode(&value)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		*out = append(*out, value)
	}
}
