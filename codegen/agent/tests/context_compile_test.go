package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/codegen/agent/tests/testscenarios"
	"github.com/CaliLuke/loom-mcp/v2/codegen/testhelpers"
	"github.com/stretchr/testify/require"
)

const contextGeneratedTest = `package contexttest

import (
 "context"
 "testing"
 "example.com/contexttest/gen/alpha/agents/compactor"
 "example.com/contexttest/gen/alpha/agents/defaults"
 "example.com/contexttest/gen/alpha/agents/disabled"
 "github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
 rt "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
)

type plan struct {}
func (plan) PlanStart(context.Context,*planner.PlanInput)(*planner.PlanResult,error) {
 return &planner.PlanResult{},nil
}
func (plan) PlanResume(context.Context,*planner.PlanResumeInput)(*planner.PlanResult,error) {
 return &planner.PlanResult{},nil
}

func TestConfigs(t *testing.T) {
 p:=plan{}
 if err:=(compactor.CompactorAgentConfig{Planner:p}).Validate();err==nil {
  t.Fatal("Summarize must require ContextModel")
 }
 if err:=defaults.RegisterDefaultsAgent(t.Context(),rt.New(),defaults.DefaultsAgentConfig{Planner:p});err!=nil {
  t.Fatal(err)
 }
 if err:=disabled.RegisterDisabledAgent(t.Context(),rt.New(),disabled.DisabledAgentConfig{Planner:p});err!=nil {
  t.Fatal(err)
 }
 if err:=compactor.RegisterCompactorAgent(t.Context(),rt.New(),compactor.CompactorAgentConfig{Planner:p,ContextModel:&rt.ContextModel{ID:"summary"}});err!=nil {
  t.Fatal(err)
 }
}
`

// TestContextGeneratedCompiles executes enabled, default and disabled config
// branches against the runtime, and vets every generated package.
func TestContextGeneratedCompiles(t *testing.T) {
	dir := t.TempDir()
	root := testRepoRoot()
	module := fmt.Sprintf("module example.com/contexttest\n\ngo 1.27.2\n\nrequire github.com/CaliLuke/loom-mcp/v2 v2.0.0\nreplace github.com/CaliLuke/loom-mcp/v2 => %s\n", root)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600))
	files := testhelpers.BuildAndGenerateWithPkg(t, "example.com/contexttest/gen", testscenarios.ContextPolicies())
	for _, file := range files {
		_, err := file.Render(dir)
		require.NoError(t, err)
	}
	writeGeneratedFile(t, dir, "context_test.go", contextGeneratedTest)
	testCmd := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "-short", "-race", "-p", "2", "./...")
	testCmd.Dir = dir
	testCmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := testCmd.CombinedOutput()
	require.NoError(t, err, "generated context tests: %s", output)
	vetCmd := exec.CommandContext(t.Context(), "go", "vet", "-mod=mod", "./...")
	vetCmd.Dir = dir
	vetCmd.Env = append(os.Environ(), "GOWORK=off")
	output, err = vetCmd.CombinedOutput()
	require.NoError(t, err, "generated context vet: %s", output)
}
