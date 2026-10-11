package agenttest_test

import (
	"os"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepeatAttemptResources(t *testing.T) {
	for _, exit := range []string{"return", "fatal", "panic", "skip"} {
		t.Run(exit, func(t *testing.T) {
			const key = "LOOM_AT1_ATTEMPT_ENV"
			t.Setenv(key, "parent")
			cwd, err := os.Getwd()
			require.NoError(t, err)
			pwd, pwdSet := os.LookupEnv("PWD")
			tb := &evalTB{TB: t}
			var dirs []string
			attempts := 0
			runTB(func() {
				agenttest.Repeat(tb, 2, 1, func(at testing.TB) {
					attempts++
					assert.Equal(t, "parent", os.Getenv(key), "next attempt environment")
					current, getErr := os.Getwd()
					require.NoError(at, getErr)
					assert.Equal(t, cwd, current, "next attempt directory")
					dir := at.TempDir()
					dirs = append(dirs, dir)
					at.Setenv(key, "attempt")
					at.Chdir(dir)
					at.Cleanup(func() {
						assert.Equal(t, "attempt", os.Getenv(key))
						assert.DirExists(t, dir)
					})
					if attempts == 1 {
						switch exit {
						case "fatal":
							at.Fatal("stop")
						case "panic":
							panic("stop")
						case "skip":
							at.Skip("stop")
						}
					}
				})
			})
			assert.Equal(t, "parent", os.Getenv(key), "environment restored before Repeat returns")
			current, err := os.Getwd()
			require.NoError(t, err)
			assert.Equal(t, cwd, current, "directory restored before Repeat returns")
			currentPWD, currentPWDSet := os.LookupEnv("PWD")
			assert.Equal(t, pwd, currentPWD)
			assert.Equal(t, pwdSet, currentPWDSet)
			for _, dir := range dirs {
				assert.NoDirExists(t, dir, "attempt temporary directory removed")
			}
		})
	}
}
