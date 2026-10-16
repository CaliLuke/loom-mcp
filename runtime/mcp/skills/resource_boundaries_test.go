package skills

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceReadPreservesMediaAndBytes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "media")
	require.NoError(t, os.Mkdir(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Media"), 0600))
	tests := []struct {
		name, mime string
		binary     bool
	}{
		{"notes.markdown", "text/markdown; charset=utf-8", false},
		{"notes.txt", "text/plain; charset=utf-8", false},
		{"data.json", "application/json", false},
		{"data.yaml", "application/yaml", false},
		{"data.yml", "application/yaml", false},
		{"data.csv", "text/csv; charset=utf-8", false},
		{"index.html", "text/html; charset=utf-8", false},
		{"style.css", "text/css; charset=utf-8", false},
		{"script.js", "text/javascript; charset=utf-8", false},
		{"image.svg", "image/svg+xml", false},
		{"image.PNG", "image/png", true},
		{"image.jpeg", "image/jpeg", true},
		{"image.gif", "image/gif", true},
		{"image.webp", "image/webp", true},
		{"document.pdf", "application/pdf", true},
		{"archive.zip", "application/zip", true},
		{"archive.gz", "application/gzip", true},
		{"config.toml", "application/toml", false},
		{"no-extension", "text/plain; charset=utf-8", false},
		{"unknown.bin", "application/octet-stream", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte("resource content")
			if tt.binary {
				data = []byte{0xff, 0, 0x80, 0x42}
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, tt.name), data, 0600))
			content, err := Read(t.Context(), []Source{{Root: root}}, "skill://media/"+tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.mime, content.MimeType)
			if tt.binary {
				require.NotNil(t, content.Blob)
				decoded, err := base64.StdEncoding.DecodeString(*content.Blob)
				require.NoError(t, err)
				assert.Equal(t, data, decoded)
				assert.Nil(t, content.Text)
			} else {
				require.NotNil(t, content.Text)
				assert.Equal(t, string(data), *content.Text)
				assert.Nil(t, content.Blob)
			}
		})
	}
}

func TestResourceDiscoveryAndReadFailures(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("not a directory"), 0600))
	for _, operation := range []string{"list", "read"} {
		t.Run(operation, func(t *testing.T) {
			var err error
			if operation == "list" {
				_, err = List(t.Context(), []Source{{Root: file}})
			} else {
				_, err = Read(t.Context(), []Source{{Root: file}}, "skill://media/SKILL.md")
			}
			require.Error(t, err)
			var pathErr *os.PathError
			assert.ErrorAs(t, err, &pathErr)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := List(ctx, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = Read(ctx, nil, "skill://media/SKILL.md")
	require.ErrorIs(t, err, context.Canceled)
	resources, err := List(t.Context(), []Source{{Root: " "}, {Root: filepath.Join(root, "missing")}, {Root: root}})
	require.NoError(t, err)
	assert.Empty(t, resources)
	_, err = Read(t.Context(), []Source{{Root: root}}, "skill://missing/SKILL.md")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestResourceRejectsInvalidMetadataAndPaths(t *testing.T) {
	for _, front := range []string{"id: bad/id", "allowed_tools: ['']", "reload: sometimes", "unknown: value", "name: [invalid]"} {
		t.Run(front, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "skill")
			require.NoError(t, os.Mkdir(dir, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+front+"\n---\n# Skill"), 0600))
			_, err := Read(t.Context(), []Source{{Root: root}}, "skill://skill/SKILL.md")
			require.ErrorIs(t, err, ErrInvalidMetadata)
		})
	}
	for _, uri := range []string{"http://skill/SKILL.md", "skill:///SKILL.md", "skill://skill", "skill://skill/.", "skill://skill/%2e%2e/file"} {
		t.Run(uri, func(t *testing.T) {
			_, err := Read(t.Context(), nil, uri)
			require.ErrorIs(t, err, ErrInvalidURI)
		})
	}
	_, err := Read(t.Context(), nil, "skill://skill/%zz")
	require.Error(t, err)
}
