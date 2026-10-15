package codegen_test

import (
	"os"
	"os/exec"
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestExportedAgentConsumersAndLoopPredicatesCompile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		design func()
	}{
		{"cross service agent toolset", consumerCompileDesign},
		{"loop predicate", predicateCompileDesign},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := generateCompileDesign(t, tc.design)
			dir := writeGeneratedModule(t, files)
			cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod -p=2")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "compile complete generated module: %s", output)
		})
	}
}

func consumerCompileDesign() {
	API("consumer-proof", func() {
	})
	Service("producer", func() {
		Agent("researcher", "Find documents", func() {
			Export("search", func() {
				Meta("owner", "research")
				Tool("find", "Find documents", func() {
					Args(func() {
						Attribute("query", String)
						Required("query")
					})
					Return(String)
					Idempotent()
					Meta("category", "lookup")
					ServerData("evidence", String, func() {
						Description("Document provenance")
						AudienceEvidence()
					})
				})
			})
		})
	})
	Service("consumer", func() {
		Agent("assistant", "Use researcher", func() {
			Use(AgentToolset("producer", "researcher", "search"))
		})
	})
}

func predicateCompileDesign() {
	API("predicate-proof", func() {
	})
	Service("alpha", func() {
		Agent("coordinator", "Run bounded workflow", func() {
			Workflow(func() {
				Loop("retry", "worker.retry", `{}`, MaxIterations(2), UntilJSONPath("retry", "$.done", "true"))
				FinalMessage("complete")
			})
		})
	})
}
