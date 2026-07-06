package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListAndReadSkillResources(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "code-review")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: Review code.\n---\n# Code Review\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "reference.md"), []byte("# Reference\n"), 0o600))

	sources := []Source{{Root: root}}
	resources, err := List(context.Background(), sources)
	require.NoError(t, err)
	require.Len(t, resources, 2)
	require.Equal(t, "skill://code-review/SKILL.md", resources[0].URI)
	require.Equal(t, "Review code.", resources[0].Description)
	require.Equal(t, "skill://code-review/_manifest", resources[1].URI)

	content, err := Read(context.Background(), sources, "skill://code-review/SKILL.md")
	require.NoError(t, err)
	require.NotNil(t, content.Text)
	require.Contains(t, *content.Text, "# Code Review")

	manifest, err := Read(context.Background(), sources, "skill://code-review/_manifest")
	require.NoError(t, err)
	require.NotNil(t, manifest.Text)
	require.Contains(t, *manifest.Text, `"path":"SKILL.md"`)
	require.Contains(t, *manifest.Text, `"path":"reference.md"`)
}

func TestStructuredFrontmatterMetadata(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "review")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
id: code-review
name: Code Review
description: Review code carefully.
allowed_tools:
  - shell
preload: on_start
reload: per_call
---
# Code Review
`), 0o600))

	resources, err := List(context.Background(), []Source{{Root: root}})
	require.NoError(t, err)
	require.Equal(t, "skill://code-review/SKILL.md", resources[0].URI)
	require.NotNil(t, resources[0].Metadata)
	require.Equal(t, "Code Review", resources[0].Name)
	require.Equal(t, []string{"shell"}, resources[0].Metadata.AllowedTools)
	require.Equal(t, PreloadOnStart, resources[0].Metadata.Preload)
	require.Equal(t, ReloadPerCall, resources[0].Metadata.Reload)

	content, err := Read(context.Background(), []Source{{Root: root}}, "skill://code-review/_manifest")
	require.NoError(t, err)
	require.NotNil(t, content.Metadata)
	require.Contains(t, *content.Text, `"id":"code-review"`)
}

func TestMissingFrontmatterDerivesMetadata(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "plain")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Plain Skill\nMore text.\n"), 0o600))

	resources, err := List(context.Background(), []Source{{Root: root}})
	require.NoError(t, err)
	require.Equal(t, "skill://plain/SKILL.md", resources[0].URI)
	require.Equal(t, "plain", resources[0].Metadata.ID)
	require.Equal(t, "Plain Skill", resources[0].Description)
}

func TestInvalidSkillMetadataReturnsError(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "bad")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\npreload: sometimes\n---\n# Bad\n"), 0o600))

	_, err := List(context.Background(), []Source{{Root: root}})
	require.ErrorIs(t, err, ErrInvalidMetadata)
}

func TestDuplicateSkillIDReturnsError(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"one", "two"} {
		skillDir := filepath.Join(root, dir)
		require.NoError(t, os.MkdirAll(skillDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nid: same\n---\n# Skill\n"), 0o600))
	}

	_, err := List(context.Background(), []Source{{Root: root}})
	require.ErrorIs(t, err, ErrInvalidMetadata)
	require.Contains(t, err.Error(), "duplicate skill id")
}

func TestReadRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "safe")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Safe\n"), 0o600))

	_, err := Read(context.Background(), []Source{{Root: root}}, "skill://safe/../secret.txt")
	require.ErrorIs(t, err, ErrInvalidURI)
}

func TestReadRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600))

	skillDir := filepath.Join(root, "safe")
	require.NoError(t, os.MkdirAll(skillDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Safe\n"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(skillDir, "secret.txt")))

	_, err := Read(context.Background(), []Source{{Root: root}}, "skill://safe/secret.txt")
	require.ErrorIs(t, err, ErrInvalidURI)
}
