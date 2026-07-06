package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestSkillToolsetRegistrationExposesSkillsAsModelTools(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "code-review")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Review code.\n---\n# Code Review\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "reference.md"), []byte("# Reference\n"), 0o600))

	rt := New()
	reg := NewSkillToolsetRegistration(SkillToolsetConfig{
		Name:  "assistant.skills",
		Roots: []string{root},
	})
	require.NoError(t, rt.RegisterToolset(reg))

	listOut, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "assistant.skills",
		ToolName:    tools.Ident("assistant.skills.list_skills"),
		ToolCallID:  "call-list",
		Payload:     rawjson.Message([]byte(`{}`)),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"skills":[{"name":"code-review","description":"Review code.","uri":"skill://code-review/SKILL.md"}]}`, string(listOut.Payload))

	loadOut, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "assistant.skills",
		ToolName:    tools.Ident("assistant.skills.load_skill_resource"),
		ToolCallID:  "call-load",
		Payload:     rawjson.Message([]byte(`{"skill":"code-review","path":"reference.md"}`)),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"uri":"skill://code-review/reference.md","mime_type":"text/plain; charset=utf-8","text":"# Reference\n"}`, string(loadOut.Payload))
}
