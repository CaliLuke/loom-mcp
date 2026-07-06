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
