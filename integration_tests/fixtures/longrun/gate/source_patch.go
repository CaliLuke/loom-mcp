package suite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// Compare retained source blobs directly with current owners. Index state is
// irrelevant: staged, unstaged and untracked changes all enter the same patch.
func captureSourcePatch(ctx context.Context, root, base string, previous, current []SourceFile) ([]byte, error) {
	old := make(map[string]SourceFile)
	now := make(map[string]SourceFile)
	paths := make([]string, 0, len(previous)+len(current))
	for _, owner := range previous {
		old[owner.Path] = owner
		paths = append(paths, owner.Path)
	}
	for _, owner := range current {
		now[owner.Path] = owner
		paths = append(paths, owner.Path)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	temporary, err := os.MkdirTemp("", "compaction-source-diff-")
	if err != nil {
		return nil, err
	}
	patch, captureErr := captureSourceChanges(ctx, root, base, temporary, paths, old, now)
	return patch, errors.Join(captureErr, os.RemoveAll(temporary))
}

func captureSourceChanges(ctx context.Context, root, base, temporary string, paths []string, old, now map[string]SourceFile) ([]byte, error) {
	var bundle []byte
	for _, path := range paths {
		before, was := old[path]
		after, is := now[path]
		if was && is && before == after {
			continue
		}
		from, to := "/dev/null", "/dev/null"
		if was {
			data, err := sourceGit(ctx, root, "show", base+":"+path)
			if err != nil {
				return nil, err
			}
			from = filepath.Join("a", path)
			if err := writeSourceSide(temporary, from, data, before.Mode); err != nil {
				return nil, err
			}
		}
		if is {
			data, err := readFile(root, path)
			if err != nil {
				return nil, err
			}
			to = filepath.Join("b", path)
			if err := writeSourceSide(temporary, to, data, after.Mode); err != nil {
				return nil, err
			}
		}
		command := exec.CommandContext(ctx, "git", "diff", "--no-index", "--binary", "--no-ext-diff", "--src-prefix=", "--dst-prefix=", "--", from, to)
		command.Dir = temporary
		patch, err := command.Output()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("capture source owner %s: %w", path, err)
		}
		bundle = append(bundle, patch...)
	}
	return bundle, nil
}

func writeSourceSide(root, path string, data []byte, mode uint32) error {
	confined, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	writeErr := confined.MkdirAll(filepath.Dir(path), 0750)
	if writeErr == nil {
		writeErr = confined.WriteFile(path, data, os.FileMode(mode))
	}
	return errors.Join(writeErr, confined.Close())
}
