package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type sourceWorkspaceKey struct{}

type listedSourceModule struct {
	Path     string
	Version  string
	Sum      string
	GoModSum string
	Dir      string
	Main     bool
	Replace  *listedSourceModule
}

type listedSourcePackage struct {
	ImportPath   string
	Dir          string
	Standard     bool
	Module       *listedSourceModule
	Error        *struct{ Err string }
	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string
	EmbedFiles   []string
}

func sourceGo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	workspace := sourceWorkspace(ctx)
	temporary := ""
	if len(args) != 0 && (args[0] == "list" || args[0] == "build" || args[0] == "run" || args[0] == "mod") {
		var err error
		workspace, temporary, err = isolatedSourceWorkspace(ctx, dir)
		if err != nil {
			return nil, err
		}
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("missing source operation")
	}
	var command *exec.Cmd
	// Only source inspection and the explicit provenance probe/build operations
	// may launch. Arguments are passed directly; no shell or executable override.
	switch args[0] {
	case "env":
		command = exec.CommandContext(ctx, "go", "env")
	case "list":
		command = exec.CommandContext(ctx, "go", "list")
	case "mod":
		command = exec.CommandContext(ctx, "go", "mod")
	case "run":
		command = exec.CommandContext(ctx, "go", "run")
	case "build":
		command = exec.CommandContext(ctx, "go", "build")
	case "tool":
		command = exec.CommandContext(ctx, "go", "tool")
	default:
		return nil, fmt.Errorf("unsupported source operation %q", args[0])
	}
	command.Args = append(command.Args, args[1:]...)
	command.Dir = dir
	command.Env = os.Environ()
	if workspace != "" && workspace != "off" {
		resolved, err := filepath.EvalSymlinks(workspace)
		if err != nil {
			if temporary != "" {
				err = errors.Join(err, os.RemoveAll(temporary))
			}
			return nil, err
		}
		command.Env = append(command.Env, "GOWORK="+resolved)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if temporary != "" {
		err = errors.Join(err, os.RemoveAll(temporary))
	}
	if err != nil {
		return nil, fmt.Errorf("source go %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return data, nil
}

func sourceEnvironment(ctx context.Context, dir, root string) (map[string]string, error) {
	keys := []string{"GOVERSION", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "GOAMD64", "GOARM64", "GOEXPERIMENT", "GOFLAGS", "CGO_ENABLED", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS", "CC", "CXX", "GOTOOLCHAIN", "GOWORK"}
	data, err := sourceGo(ctx, dir, append([]string{"env", "-json"}, keys...)...)
	if err != nil {
		return nil, err
	}
	var environment map[string]string
	if err := json.Unmarshal(data, &environment); err != nil {
		return nil, err
	}
	if environment["CGO_ENABLED"] != "0" {
		return nil, fmt.Errorf("source evidence requires explicit CGO_ENABLED=0; native compiler inputs are unsupported")
	}
	workspace, err := filepath.EvalSymlinks(environment["GOWORK"])
	if err != nil {
		return nil, err
	}
	if workspace != filepath.Join(root, "go.work") {
		return nil, fmt.Errorf("source command must use the declared repository workspace")
	}
	environment["GOWORK"] = "go.work"
	// Unknown compiler/overlay/linker substitutions need their own recoverable
	// input closure. Refuse them rather than erase a semantic build flag.
	for _, flag := range strings.Fields(environment["GOFLAGS"]) {
		if flag == "-race" || flag == "-trimpath" || strings.HasPrefix(flag, "-p=") || strings.HasPrefix(flag, "-tags=") || strings.HasPrefix(flag, "-buildvcs=") || flag == "-mod=readonly" {
			continue
		}
		return nil, fmt.Errorf("unsupported source build flag %q", flag)
	}
	for key, value := range environment {
		if strings.Contains(value, root) {
			return nil, fmt.Errorf("unsupported path-bearing source setting %s", key)
		}
	}
	return environment, nil
}

func sourceModuleIdentity(root, cache string, selected listedSourceModule) (SourceModule, error) {
	out := SourceModule{Path: selected.Path, Version: selected.Version, Sum: selected.Sum, GoModSum: selected.GoModSum}
	if selected.Main || (selected.Version == "" && selected.Dir != "") {
		local, err := sourceRelative(root, selected.Dir)
		if err != nil {
			return out, err
		}
		out.Local = local
		if filepath.IsAbs(out.Path) || strings.HasPrefix(out.Path, ".") {
			out.Path = "local:" + local
		}
	}
	if selected.Replace != nil {
		replacement, err := sourceModuleIdentity(root, cache, *selected.Replace)
		if err != nil {
			return out, err
		}
		out.Replace = &replacement
	}
	if out.Version != "" && out.Replace == nil {
		escapedPath, err := module.EscapePath(out.Path)
		if err != nil {
			return out, err
		}
		escapedVersion, err := module.EscapeVersion(out.Version)
		if err != nil {
			return out, err
		}
		modPath := filepath.Join(cache, "cache/download", escapedPath, "@v", escapedVersion+".mod")
		checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
			return os.OpenInRoot(filepath.Dir(modPath), filepath.Base(modPath))
		})
		if err != nil {
			return out, fmt.Errorf("selected module %s go.mod checksum: %w", out.Path, err)
		}
		if out.GoModSum != "" && out.GoModSum != checksum {
			return out, fmt.Errorf("selected module %s go.mod checksum mismatch", out.Path)
		}
		out.GoModSum = checksum
	}
	return out, nil
}

func (p listedSourcePackage) files() []string {
	var files []string
	for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.CFiles, p.CXXFiles, p.MFiles, p.HFiles, p.FFiles, p.SFiles, p.SwigFiles, p.SwigCXXFiles, p.SysoFiles, p.EmbedFiles} {
		files = append(files, group...)
	}
	return files
}

func sourceWorkspace(ctx context.Context) string {
	if workspace, ok := ctx.Value(sourceWorkspaceKey{}).(string); ok {
		return workspace
	}
	return os.Getenv("GOWORK")
}

func selectedSourceSums(cache string, selected *SourceModule, used map[string]bool) error {
	if selected.Replace != nil {
		selected.Sum = ""
		return selectedSourceSums(cache, selected.Replace, used)
	}
	if selected.Version == "" || !used[selected.Path+"@"+selected.Version] {
		selected.Sum = ""
		return nil
	}
	escapedPath, err := module.EscapePath(selected.Path)
	if err != nil {
		return err
	}
	escapedVersion, err := module.EscapeVersion(selected.Version)
	if err != nil {
		return err
	}
	data, err := readFile(cache, filepath.Join("cache/download", escapedPath, "@v", escapedVersion+".ziphash"))
	if err != nil {
		return fmt.Errorf("selected executable module %s content checksum: %w", selected.Path, err)
	}
	checksum := strings.TrimSpace(string(data))
	if !strings.HasPrefix(checksum, "h1:") || (selected.Sum != "" && selected.Sum != checksum) {
		return fmt.Errorf("selected executable module %s content checksum mismatch", selected.Path)
	}
	selected.Sum = checksum
	return nil
}
