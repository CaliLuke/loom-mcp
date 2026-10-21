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
 "errors"
 "testing"
 "example.com/contexttest/gen/alpha/agents/compactor"
 "example.com/contexttest/gen/alpha/agents/defaults"
 "example.com/contexttest/gen/alpha/agents/disabled"
 "example.com/contexttest/gen/alpha/agents/implicit"
 "github.com/CaliLuke/loom-mcp/v2/agenttest"
 "github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
 "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
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

func TestEffectivePolicies(t *testing.T) {
 for _, tc := range []struct {
  name string
  id agent.Ident
  register func(context.Context,*rt.Runtime) error
  options []rt.RuntimeOption
  def, threshold, summary, disabledRecall bool
 }{
  {name:"implicit nil", id:implicit.AgentID, def:true, register:func(ctx context.Context,r *rt.Runtime)error {
   return implicit.RegisterImplicitAgent(ctx,r,implicit.ImplicitAgentConfig{Planner:plan{}})
  }},
  {name:"implicit model", id:implicit.AgentID, def:true, summary:true, register:func(ctx context.Context,r *rt.Runtime)error {
   return implicit.RegisterImplicitAgent(ctx,r,implicit.ImplicitAgentConfig{Planner:plan{},ContextModel:&rt.ContextModel{ID:"summary"}})
  }},
  {name:"disabled", id:disabled.AgentID, disabledRecall:true, register:func(ctx context.Context,r *rt.Runtime)error {
   return disabled.RegisterDisabledAgent(ctx,r,disabled.DisabledAgentConfig{Planner:plan{}})
  }},
  {name:"explicit empty", id:defaults.AgentID, threshold:true, register:func(ctx context.Context,r *rt.Runtime)error {
   return defaults.RegisterDefaultsAgent(ctx,r,defaults.DefaultsAgentConfig{Planner:plan{}})
  }},
  {name:"summary", id:compactor.AgentID, threshold:true, summary:true, disabledRecall:true, register:func(ctx context.Context,r *rt.Runtime)error {
   return compactor.RegisterCompactorAgent(ctx,r,compactor.CompactorAgentConfig{Planner:plan{},ContextModel:&rt.ContextModel{ID:"summary"}})
  }},
  {name:"runtime overrides disabled", id:disabled.AgentID, threshold:true, disabledRecall:true,
   options:[]rt.RuntimeOption{rt.WithContextCompaction(disabled.AgentID,rt.ContextCompaction{KeepRecentTokens:777})},
   register:func(ctx context.Context,r *rt.Runtime)error {
    return disabled.RegisterDisabledAgent(ctx,r,disabled.DisabledAgentConfig{Planner:plan{}})
  }},
  {name:"runtime overrides default", id:implicit.AgentID, threshold:true,
   options:[]rt.RuntimeOption{rt.WithContextCompaction(implicit.AgentID,rt.ContextCompaction{KeepRecentTokens:777})},
   register:func(ctx context.Context,r *rt.Runtime)error {
    return implicit.RegisterImplicitAgent(ctx,r,implicit.ImplicitAgentConfig{Planner:plan{}})
  }},
 } {
  t.Run(tc.name,func(t *testing.T){
   stop:=errors.New("observed run input")
   seen:=make(chan api.RunInput,1)
   opts:=append([]rt.RuntimeOption{},tc.options...)
   opts=append(opts,rt.WithInterceptors(rt.RuntimeInterceptorFuncs{BeforeRunFunc:func(_ context.Context,in *rt.BeforeRunInput)(*rt.BeforeRunDecision,error){
    seen<-in.Input
    return nil,stop
   }}))
   runtime:=rt.New(opts...)
   client,_:=agenttest.ScriptClient(t)
   require.NoError(t,runtime.RegisterModel("summary",client))
   require.NoError(t,tc.register(t.Context(),runtime))
   _,err:=runtime.CreateSession(t.Context(),"session")
   require.NoError(t,err)
   _,err=runtime.MustClient(tc.id).Run(t.Context(),"session",nil,rt.WithRunID("run"))
   require.ErrorIs(t,err,stop)
   input:=<-seen
   assert.Equal(t,tc.def,input.ContextSettings.Default)
   assert.Equal(t,tc.threshold,input.ContextSettings.Threshold)
   assert.Equal(t,tc.summary,input.ContextSettings.Summary.Enabled)
   assert.Equal(t,tc.disabledRecall,input.ContextRecovery.DisableRecall)
   assert.NotEmpty(t,input.ContextFingerprint)
   if len(tc.options)>0 {
    require.NotNil(t,input.ContextCompaction)
    assert.Equal(t,777,input.ContextCompaction.KeepRecentTokens)
   }
  })
 }
}

`

// TestContextGeneratedCompiles executes enabled, default and disabled config
// branches against the runtime, and vets every generated package.
func TestContextGeneratedCompiles(t *testing.T) {
	dir := t.TempDir()
	root := testRepoRoot()
	module := fmt.Sprintf("module example.com/contexttest\n\ngo 1.27.3\n\nrequire github.com/CaliLuke/loom-mcp/v2 v2.0.0\nreplace github.com/CaliLuke/loom-mcp/v2 => %s\n", root)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600))
	require.NoError(t, rewriteFixtureGoMod(root, dir))
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
