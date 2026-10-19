package testfixture_test

import (
	"context"
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/eval/testfixture"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

const bindingSkill = "---\nid: transform\nname: Transform\ndescription: Run the installed helper.\n---\nUse components.run_helper.\n"
const bindingSource = `package main

import (
 "fmt"
 "io"
 "os"
 "strings"
)

func main() {
 input,err:=io.ReadAll(os.Stdin)
 if err!=nil {
  panic(err)
 }
 fmt.Print(strings.ToUpper(string(input)))
}
`

func TestInstallComponentBindingsExecutesExactSource(t *testing.T) {
	dir := t.TempDir()
	bindings, err := testfixture.InstallComponentBindings(t.Context(), dir, []byte(bindingSource), []byte(bindingSkill))
	require.NoError(t, err)
	require.NotNil(t, bindings.Build)
	assert.Len(t, bindings.Build.ExecutableDigest, 64)
	require.Len(t, bindings.Toolsets, 2)
	assert.Equal(t, "skills", bindings.Toolsets[0].Name)
	assert.Equal(t, "components", bindings.Toolsets[1].Name)
	loaded, err := bindings.Toolsets[0].Execute(t.Context(), &planner.ToolRequest{Name: "skills.load_skill", ToolCallID: "skill", Payload: rawjson.Message(`{"skill":"transform"}`)})
	require.NoError(t, err)
	encoded, err := json.Marshal(loaded.ToolResult.Result)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "components.run_helper")
	output, err := bindings.Toolsets[1].Execute(t.Context(), &planner.ToolRequest{Name: "components.run_helper", ToolCallID: "helper", Payload: rawjson.Message(`{"input":"fresh π"}`)})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"stdout": "FRESH Π"}, output.ToolResult.Result)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	source, err := root.ReadFile("main.go")
	require.NoError(t, err)
	assert.Equal(t, bindingSource, string(source))
	skill, err := root.ReadFile("skills/transform/SKILL.md")
	require.NoError(t, err)
	assert.Equal(t, bindingSkill, string(skill))
	require.NoError(t, root.Close())
}

func TestInstallComponentBindingsFailureLeavesCleanupWithCaller(t *testing.T) {
	for _, tc := range []struct{ name, source, skill string }{
		{"invalid_source", "package main\nfunc broken(", bindingSkill},
		{"invalid_skill", bindingSource, "---\nid: [\n---\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bindings, err := testfixture.InstallComponentBindings(t.Context(), dir, []byte(tc.source), []byte(tc.skill))
			require.Error(t, err)
			assert.Nil(t, bindings.Build)
			assert.Empty(t, bindings.Toolsets)
			stat, err := os.Stat(dir)
			require.NoError(t, err)
			assert.True(t, stat.IsDir())
		})
	}
	t.Run("occupied_directory", func(t *testing.T) {
		dir := t.TempDir()
		root, err := os.OpenRoot(dir)
		require.NoError(t, err)
		require.NoError(t, root.WriteFile("owned.txt", []byte("existing owner"), 0o600))
		bindings, err := testfixture.InstallComponentBindings(t.Context(), dir, []byte(bindingSource), []byte(bindingSkill))
		require.Error(t, err)
		assert.Nil(t, bindings.Build)
		assert.Empty(t, bindings.Toolsets)
		retained, err := root.ReadFile("owned.txt")
		require.NoError(t, err)
		assert.Equal(t, "existing owner", string(retained))
		require.NoError(t, root.Close())
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dir := t.TempDir()
	_, err := testfixture.InstallComponentBindings(ctx, dir, []byte(bindingSource), []byte(bindingSkill))
	require.ErrorIs(t, err, context.Canceled)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
