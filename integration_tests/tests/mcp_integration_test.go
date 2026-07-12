package tests

import (
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/integration_tests/framework"
	"github.com/stretchr/testify/require"
)

func requireServer(t *testing.T) {
	t.Helper()
	if !framework.SupportsServer() {
		t.Skip("integration server not available; set TEST_SERVER_URL or restore the example directory")
	}
}

func requireCLI(t *testing.T) {
	t.Helper()
	if !framework.SupportsCLI() {
		t.Skip("integration CLI not available; restore the example directory to run CLI scenarios")
	}
}

func TestMCPProtocol(t *testing.T) {
	t.Parallel()
	runScenarioFile(t, "../scenarios/protocol.yaml")
}

func TestMCPTools(t *testing.T) {
	t.Parallel()
	runScenarioFile(t, "../scenarios/tools.yaml")
}

func TestMCPResources(t *testing.T) {
	t.Parallel()
	runScenarioFile(t, "../scenarios/resources.yaml")
}

func TestMCPPrompts(t *testing.T) {
	t.Parallel()
	runScenarioFile(t, "../scenarios/prompts.yaml")
}

func TestMCPPromptsCLI(t *testing.T) {
	t.Parallel()
	if os.Getenv("MCP_CLI_TESTS") != "true" {
		t.Skip("CLI tests disabled; set MCP_CLI_TESTS=true to enable")
	}
	requireCLI(t)
	runScenarioFile(t, "../scenarios/prompts_cli.yaml")
}

func TestMCPNotifications(t *testing.T) {
	t.Parallel()
	runScenarioFile(t, "../scenarios/notifications.yaml")
}

func runScenarioFile(t *testing.T, path string) {
	t.Helper()
	requireServer(t)
	scenarios, err := framework.LoadScenarios(path)
	require.NoError(t, err)
	for _, sc := range scenarios {
		scenario := sc
		t.Run(scenario.Name, func(t *testing.T) {
			runner := framework.NewRunner()
			require.NoError(t, runner.Run(t, []framework.Scenario{scenario}))
		})
	}
}
