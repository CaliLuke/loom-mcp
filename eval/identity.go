package eval

import (
	"context"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"
)

// Identity records source provenance and compiled dependency versions.
type Identity struct {
	Source   VCS
	Versions Versions
}

// VCS records source control evidence and where it was detected.
type VCS struct {
	System   string `json:"system"`
	Revision string `json:"revision"`
	Modified bool   `json:"modified"`
	Origin   string `json:"origin"`
}

// Versions records the Go toolchain and relevant module versions.
type Versions struct {
	Go          string `json:"go"`
	MainModule  string `json:"main_module"`
	MainVersion string `json:"main_version"`
	LoomMCP     string `json:"loom_mcp"`
	Loom        string `json:"loom"`
}

const originNone = "none"

// DetectIdentity reads build provenance, then tries git with a five-second deadline.
// Missing source metadata never fails an experiment.
func DetectIdentity(ctx context.Context, dir string) Identity {
	info, ok := debug.ReadBuildInfo()
	var identity Identity
	if ok {
		identity = identityFromBuildInfo(info)
	} else {
		identity = identityFromBuildInfo(nil)
	}
	if identity.Source.Revision != "" {
		return identity
	}
	identity.Source = detectGit(ctx, dir)
	return identity
}

func identityFromBuildInfo(info *debug.BuildInfo) Identity {
	identity := Identity{Source: VCS{Origin: originNone}}
	if info == nil {
		return identity
	}
	identity.Versions = Versions{Go: info.GoVersion, MainModule: info.Main.Path, MainVersion: info.Main.Version}
	modules := append([]*debug.Module{&info.Main}, info.Deps...)
	for _, module := range modules {
		version := module.Version
		if module.Replace != nil {
			version = module.Replace.Version
		}
		switch module.Path {
		case "github.com/CaliLuke/loom-mcp/v2":
			identity.Versions.LoomMCP = version
		case "github.com/CaliLuke/loom":
			identity.Versions.Loom = version
		}
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs":
			identity.Source.System = setting.Value
		case "vcs.revision":
			identity.Source.Revision = setting.Value
		case "vcs.modified":
			identity.Source.Modified = setting.Value == "true"
		}
	}
	if identity.Source.Revision != "" {
		identity.Source.Origin = "buildinfo"
	}
	return identity
}

func detectGit(ctx context.Context, dir string) VCS {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	revisionCmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	revisionCmd.Dir = dir
	revision, err := revisionCmd.Output()
	if err != nil {
		return VCS{Origin: originNone}
	}
	statusCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "status", "--porcelain")
	statusCmd.Dir = dir
	status, err := statusCmd.Output()
	if err != nil {
		return VCS{Origin: originNone}
	}
	return VCS{System: "git", Revision: strings.TrimSpace(string(revision)), Modified: len(status) > 0, Origin: "git"}
}
