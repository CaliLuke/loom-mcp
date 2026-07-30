package tests

import (
	"fmt"
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/integration_tests/framework"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := framework.CleanupTestArtifacts(); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup integration test artifacts: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
