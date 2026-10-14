package suite

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SourceExecutableID reads the current executable's Go build identity without
// invoking any fixture, provider, experiment store or artifact writer.
func SourceExecutableID(ctx context.Context) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return sourceBuildID(ctx, "", executable)
}

// VerifySourceExecutable checks the actual executable against a fresh build of
// current owners. Both canonical go-run and ordinary compiled commands work.
// Build IDs are transient execution checks, never semantic manifest identities.
func VerifySourceExecutable(ctx context.Context, root, executable string) error {
	info, err := buildinfo.ReadFile(executable)
	if err != nil {
		return fmt.Errorf("read source executable build settings: %w", err)
	}
	pureGo := false
	for _, setting := range info.Settings {
		if setting.Key == "CGO_ENABLED" && setting.Value == "0" {
			pureGo = true
		}
	}
	if !pureGo {
		return fmt.Errorf("source evidence executable must be built with CGO_ENABLED=0")
	}
	before, err := SourceSnapshot(ctx, root)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "integration_tests/fixtures/longrun")
	actual, err := sourceBuildID(ctx, dir, executable)
	if err != nil {
		return err
	}
	// The probe's early command branch only reads its own executable identity.
	// go run's debug omission is not equivalent to an ordinary go build.
	probe, err := sourceGo(ctx, dir, "run", "./cmd/compaction-gate", "_source-executable")
	if err != nil {
		return err
	}
	expected := strings.TrimSpace(string(probe))
	if !validSourceBuildID(expected) {
		return fmt.Errorf("invalid source executable probe response")
	}
	if actual != expected {
		temporary, err := os.MkdirTemp("", "compaction-source-build-")
		if err != nil {
			return err
		}
		candidate := filepath.Join(temporary, "compaction-gate")
		if _, err := sourceGo(ctx, dir, "build", "-o", candidate, "./cmd/compaction-gate"); err != nil {
			cleanupErr := os.RemoveAll(temporary)
			return errors.Join(err, cleanupErr)
		}
		expected, err = sourceBuildID(ctx, dir, candidate)
		cleanupErr := os.RemoveAll(temporary)
		if err != nil || cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
	}
	after, err := SourceSnapshot(ctx, root)
	if err != nil {
		return err
	}
	if before.SHA256 != after.SHA256 {
		return fmt.Errorf("source changed during executable verification")
	}
	if actual != expected {
		return fmt.Errorf("executable does not match current source and build inputs; rebuild before writing evidence")
	}
	return nil
}

func sourceBuildID(ctx context.Context, dir, executable string) (string, error) {
	data, err := sourceGo(ctx, dir, "tool", "buildid", executable)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !validSourceBuildID(id) {
		return "", fmt.Errorf("missing or malformed Go executable build identity")
	}
	return id, nil
}

func validSourceBuildID(id string) bool {
	return id != "" && !strings.ContainsAny(id, " \t\r\n") && strings.Count(id, "/") == 3
}
