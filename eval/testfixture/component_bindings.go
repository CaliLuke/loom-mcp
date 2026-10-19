package testfixture

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	mcpskills "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/skills"
)

// ComponentBindings retains installation identity and toolsets for one host directory.
type ComponentBindings struct {
	// Build identifies the compiler, dependency contract and exact executable bytes.
	Build *eval.PreparedBuild
	// Toolsets contains the isolated real skill and helper registrations, in that order.
	Toolsets []runtime.ToolsetRegistration
}

// InstallComponentBindings installs exact helper/skill bytes without registering a solver.
// The caller supplies a fresh existing directory and retains cleanup ownership on error.
func InstallComponentBindings(ctx context.Context, dir string, source, skill []byte) (ComponentBindings, error) {
	if err := ctx.Err(); err != nil {
		return ComponentBindings{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ComponentBindings{}, err
	}
	if len(entries) != 0 {
		return ComponentBindings{}, errors.New("component: installation directory must be fresh")
	}
	skillRoot := filepath.Join(dir, "skills")
	if err := os.MkdirAll(filepath.Join(skillRoot, "transform"), 0o700); err != nil {
		return ComponentBindings{}, err
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "transform", "SKILL.md"), skill, 0o600); err != nil {
		return ComponentBindings{}, err
	}
	if _, err := mcpskills.Read(ctx, []mcpskills.Source{{Root: skillRoot}}, "skill://transform/SKILL.md"); err != nil {
		return ComponentBindings{}, err
	}
	build, err := compileComponent(ctx, dir, source)
	if err != nil {
		return ComponentBindings{}, err
	}
	skills := runtime.NewSkillToolsetRegistration(runtime.SkillToolsetConfig{Name: "skills", Roots: []string{skillRoot}, Preload: runtime.SkillPreloadNone, Reload: runtime.SkillReloadNever})
	return ComponentBindings{Build: build, Toolsets: []runtime.ToolsetRegistration{skills, componentHelperRegistration(dir)}}, nil
}

// readComponentFile confines artifact reads to the acquired environment.
func readComponentFile(dir, name string) (data []byte, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, root.Close())
	}()
	return root.ReadFile(name)
}

// compileComponent writes exactly the submitted source and builds it without modules.
func compileComponent(ctx context.Context, dir string, source []byte) (*eval.PreparedBuild, error) {
	sourcePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", "helper", "main.go")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=off", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compile submitted helper: %w: %s", err, output)
	}
	compiler, err := exec.CommandContext(ctx, "go", "version").Output()
	if err != nil {
		return nil, err
	}
	binary, err := readComponentFile(dir, "helper")
	if err != nil {
		return nil, err
	}
	return &eval.PreparedBuild{Compiler: strings.TrimSpace(string(compiler)), Dependencies: "stdlib only; " + strings.TrimSpace(string(compiler)), ExecutableDigest: componentDigest(binary)}, nil
}

// componentHelperRegistration invokes only the executable installed in this lease.
func componentHelperRegistration(dir string) runtime.ToolsetRegistration {
	helper := tools.ToolSpec{Name: "components.run_helper", Toolset: "components", Description: "Execute the installed helper using the task input.", Payload: tools.TypeSpec{Codec: tools.AnyJSONCodec}, Result: tools.TypeSpec{Codec: tools.AnyJSONCodec}}
	return runtime.ToolsetRegistration{Name: "components", Specs: []tools.ToolSpec{helper}, Execute: func(ctx context.Context, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
		var payload struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal(call.Payload, &payload); err != nil {
			return nil, err
		}
		command := exec.CommandContext(ctx, "./helper")
		command.Dir = dir
		command.Stdin = strings.NewReader(payload.Input)
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("execute submitted helper: %w", err)
		}
		return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"stdout": string(output)}}), nil
	}}
}
