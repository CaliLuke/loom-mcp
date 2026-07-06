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
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nid: code-review\nname: Code Review\ndescription: Review code.\nallowed_tools:\n  - shell\npreload: on_start\nreload: per_call\n---\n# Code Review\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "reference.md"), []byte("# Reference\n"), 0o600))

	rt := New()
	reg := NewSkillToolsetRegistration(SkillToolsetConfig{
		Name:    "assistant.skills",
		Roots:   []string{root},
		Preload: SkillPreloadOnStart,
		Reload:  SkillReloadPerCall,
	})
	require.NoError(t, rt.RegisterToolset(reg))

	listOut, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "assistant.skills",
		ToolName:    tools.Ident("assistant.skills.list_skills"),
		ToolCallID:  "call-list",
		Payload:     rawjson.Message([]byte(`{}`)),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"skills":[{"id":"code-review","name":"Code Review","description":"Review code.","uri":"skill://code-review/SKILL.md","allowed_tools":["shell"],"preload":"on_start","reload":"per_call","metadata":{"id":"code-review","name":"Code Review","description":"Review code.","allowed_tools":["shell"],"preload":"on_start","reload":"per_call"},"preloaded":true}]}`, string(listOut.Payload))

	loadOut, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "assistant.skills",
		ToolName:    tools.Ident("assistant.skills.load_skill_resource"),
		ToolCallID:  "call-load",
		Payload:     rawjson.Message([]byte(`{"skill":"code-review","path":"reference.md"}`)),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"uri":"skill://code-review/reference.md","mime_type":"text/plain; charset=utf-8","text":"# Reference\n","metadata":{"id":"code-review","name":"Code Review","description":"Review code.","allowed_tools":["shell"],"preload":"on_start","reload":"per_call"},"reloaded":true}`, string(loadOut.Payload))
}

func TestSkillToolsetHonorsPerSkillReloadMetadata(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "code-review")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nid: code-review\nname: Code Review\nreload: per_call\n---\n# First\n"), 0o600))

	rt := New()
	reg := NewSkillToolsetRegistration(SkillToolsetConfig{
		Name:   "assistant.skills",
		Roots:  []string{root},
		Reload: SkillReloadNever,
	})
	require.NoError(t, rt.RegisterToolset(reg))

	payload := rawjson.Message([]byte(`{"skill":"code-review"}`))
	first, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "assistant.skills",
		ToolName:    tools.Ident("assistant.skills.load_skill"),
		ToolCallID:  "call-first",
		Payload:     payload,
	})
	require.NoError(t, err)
	require.Contains(t, string(first.Payload), "# First")

	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nid: code-review\nname: Code Review\nreload: per_call\n---\n# Second\n"), 0o600))
	second, err := rt.ExecuteToolActivity(context.Background(), &ToolInput{
		ToolsetName: "assistant.skills",
		ToolName:    tools.Ident("assistant.skills.load_skill"),
		ToolCallID:  "call-second",
		Payload:     payload,
	})
	require.NoError(t, err)
	require.Contains(t, string(second.Payload), "# Second")
	require.Contains(t, string(second.Payload), `"reloaded":true`)
}
