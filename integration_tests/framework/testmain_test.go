package framework

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := CleanupTestArtifacts(); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup integration framework artifacts: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
