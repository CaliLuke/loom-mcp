package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillDiscoverySortsIdentitiesAndOwnsMetadata(t *testing.T) {
	root := t.TempDir()
	for _, file := range []struct{ dir, body string }{
		{"first", "---\nid: z-last\nallowed_tools: [read]\n---\n# Last"},
		{"second", "---\nid: a-first\nallowed_tools: [write]\n---\n# First"},
		{"empty", ""},
	} {
		dir := filepath.Join(root, file.dir)
		require.NoError(t, os.Mkdir(dir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(file.body), 0600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(root, "not-a-skill"), 0700))
	resources, err := List(t.Context(), []Source{{Root: root}})
	require.NoError(t, err)
	require.Len(t, resources, 6)
	assert.Equal(t, "skill://a-first/SKILL.md", resources[0].URI)
	assert.Equal(t, "First", resources[0].Description)
	assert.Equal(t, "skill://empty/SKILL.md", resources[2].URI)
	assert.Empty(t, resources[2].Description)
	assert.Equal(t, "skill://z-last/SKILL.md", resources[4].URI)
	meta := MetadataMeta(resources[0].Metadata)
	require.NotNil(t, meta)
	meta["skill"].(map[string]any)["allowed_tools"].([]string)[0] = "changed"
	resources[0].Metadata.AllowedTools[0] = "other"
	assert.Equal(t, []string{"write"}, resources[1].Metadata.AllowedTools)
	assert.Nil(t, MetadataMeta(nil))
}

func TestSkillDiscoveryRejectsUnterminatedFrontmatterAndSymlinkMain(t *testing.T) {
	for _, name := range []string{"unterminated", "symlink"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "skill")
			require.NoError(t, os.Mkdir(dir, 0700))
			if name == "unterminated" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nid: missing-end"), 0600))
			} else {
				outside := filepath.Join(t.TempDir(), "outside.md")
				require.NoError(t, os.WriteFile(outside, []byte("private"), 0600))
				require.NoError(t, os.Symlink(outside, filepath.Join(dir, "SKILL.md")))
			}
			resources, err := List(t.Context(), []Source{{Root: root}})
			if name == "unterminated" {
				require.ErrorIs(t, err, ErrInvalidMetadata)
			} else {
				require.ErrorIs(t, err, ErrInvalidURI)
			}
			assert.Nil(t, resources)
		})
	}
}
