package suite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// Go's readonly module mode still writes go.work.sum. Resolve through an
// equivalent disposable workspace so auxiliary checksum maintenance cannot
// modify the user's files. Module use/replacement targets stay exactly pinned.
func isolatedSourceWorkspace(ctx context.Context, dir string) (string, string, error) {
	data, err := sourceGo(ctx, dir, "env", "GOWORK")
	if err != nil {
		return "", "", err
	}
	original := strings.TrimSpace(string(data))
	if original == "" || original == "off" {
		return "", "", fmt.Errorf("source resolution requires a workspace")
	}
	original, err = filepath.EvalSymlinks(original)
	if err != nil {
		return "", "", err
	}
	source, err := os.ReadFile(original)
	if err != nil {
		return "", "", err
	}
	workspace, err := modfile.ParseWork(original, source, nil)
	if err != nil {
		return "", "", err
	}
	for _, use := range append([]*modfile.Use(nil), workspace.Use...) {
		path := use.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(original), path)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", "", err
		}
		if err := workspace.DropUse(use.Path); err != nil {
			return "", "", err
		}
		if err := workspace.AddUse(path, use.ModulePath); err != nil {
			return "", "", err
		}
	}
	for _, replacement := range append([]*modfile.Replace(nil), workspace.Replace...) {
		if replacement.New.Version != "" {
			continue
		}
		path := replacement.New.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(original), path)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", "", err
		}
		if err := workspace.AddReplace(replacement.Old.Path, replacement.Old.Version, path, ""); err != nil {
			return "", "", err
		}
	}
	workspace.Cleanup()
	temporary, err := os.MkdirTemp("", "compaction-source-workspace-")
	if err != nil {
		return "", "", err
	}
	target := filepath.Join(temporary, "go.work")
	if err := writeSourceSide(temporary, "go.work", modfile.Format(workspace.Syntax), 0600); err != nil {
		return "", "", errors.Join(err, os.RemoveAll(temporary))
	}
	sums, err := readFile(filepath.Dir(original), filepath.Base(original)+".sum")
	if err == nil {
		err = writeSourceSide(temporary, "go.work.sum", sums, 0600)
	}
	if err != nil && !os.IsNotExist(err) {
		return "", "", errors.Join(err, os.RemoveAll(temporary))
	}
	return target, temporary, nil
}
