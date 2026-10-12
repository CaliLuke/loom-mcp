package agenttest_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resourceTB struct {
	*evalTB
	output bytes.Buffer
	attrs  []string
}

func TestRepeatAttemptMetadata(t *testing.T) {
	tb := &resourceTB{evalTB: &evalTB{TB: t}}
	runTB(func() {
		agenttest.Repeat(tb, 1, 1, func(at testing.TB) {
			at.Cleanup(func() { assert.True(t, at.Skipped()) })
			_, err := fmt.Fprint(at.Output(), "local output")
			require.NoError(t, err)
			at.Attr("case", "local")
			at.Skip("skip")
		})
	})
	assert.Empty(t, tb.attrs, "attempt attributes must not become parent attributes")
	assert.Empty(t, tb.output.String(), "attempt output must use attempt logs")
	assert.Contains(t, strings.Join(tb.logs, "\n"), "attempt 1/1:")
	assert.Contains(t, strings.Join(tb.logs, "\n"), "local output")
	assert.Contains(t, strings.Join(tb.logs, "\n"), "case=local")
}

func TestRepeatResourceFailuresStayInAttempt(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(testing.TB)
	}{
		{"setenv", func(at testing.TB) { at.Setenv("invalid=key", "value") }},
		{"chdir", func(at testing.TB) { at.Chdir(filepath.Join(t.TempDir(), "missing")) }},
		{"attr", func(at testing.TB) { at.Attr("invalid key", "value") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tb := &resourceTB{evalTB: &evalTB{TB: t}}
			attempts := 0
			report := agenttest.Repeat(tb, 2, 1, func(at testing.TB) {
				attempts++
				if attempts == 1 {
					tc.fail(at)
				}
			})
			assert.False(t, tb.failed)
			assert.Equal(t, 1, report.Passed)
			require.Len(t, report.Failed, 1)
			assert.Equal(t, 1, report.Failed[0].Index)
		})
	}
}

func TestRepeatProcessStateRetainsParallelRestriction(t *testing.T) {
	for _, operation := range []string{"setenv", "chdir"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			t.Parallel()
			tb := &evalTB{TB: t}
			attempts := 0
			report := agenttest.Repeat(tb, 2, 1, func(at testing.TB) {
				attempts++
				if attempts != 1 {
					return
				}
				switch operation {
				case "setenv":
					at.Setenv("FA1_MUST_NOT_SET", "changed")
				case "chdir":
					at.Chdir(dir)
				}
			})
			assert.False(t, tb.failed)
			assert.Equal(t, 1, report.Passed)
			require.Len(t, report.Failed, 1)
			assert.Contains(t, strings.Join(report.Failed[0].Diagnostics, "\n"), "t.Parallel")
		})
	}
}

func (t *resourceTB) Setenv(key, value string) {
	previous, present := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatal(err)
	}
	t.TB.Cleanup(func() {
		if present {
			assert.NoError(t.TB, os.Setenv(key, previous))
		} else {
			assert.NoError(t.TB, os.Unsetenv(key))
		}
	})
}
func (t *resourceTB) Chdir(dir string) {
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	t.TB.Chdir(dir)
}
func (t *resourceTB) Skipped() bool     { return t.skipped }
func (t *resourceTB) Output() io.Writer { return &t.output }
func (t *resourceTB) Attr(key, value string) {
	if strings.Contains(key, " ") {
		t.Error("invalid key")
		return
	}
	t.attrs = append(t.attrs, key+"="+value)
}
