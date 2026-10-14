package suite

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// WriteSource records recoverable source only after binding the running CLI to it.
func WriteSource(ctx context.Context, dir, configuration, base string) (SourceManifest, error) {
	if err := validateConfigurationName(configuration); err != nil {
		return SourceManifest{}, err
	}
	path := sourceManifestPath(dir, configuration)
	if _, err := os.Stat(path); err == nil {
		return SourceManifest{}, fmt.Errorf("source manifest already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return SourceManifest{}, err
	}
	root, err := EvidenceRoot(dir)
	if err != nil {
		return SourceManifest{}, err
	}
	if err := VerifyEvidenceExecutable(ctx, dir); err != nil {
		return SourceManifest{}, err
	}
	manifest, bundle, err := BuildSourceManifest(ctx, root, configuration, base)
	if err != nil {
		return manifest, err
	}
	bundlePath := sourceBundlePath(manifest.Identity.SHA256)
	if err := writeOnce(filepath.Join(dir, bundlePath), bundle); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return manifest, err
		}
		retained, readErr := readFile(dir, bundlePath)
		if readErr != nil {
			return manifest, readErr
		}
		if !bytes.Equal(retained, bundle) {
			return manifest, fmt.Errorf("retained source bundle differs")
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	return manifest, writeOnce(path, append(data, '\n'))
}

// ReadSource verifies retained source and its recovery bundle against current owners.
func ReadSource(ctx context.Context, dir, configuration string) (SourceManifest, error) {
	var manifest SourceManifest
	if err := validateConfigurationName(configuration); err != nil {
		return manifest, err
	}
	data, err := readFile(dir, filepath.Join("gate", filepath.Base(sourceManifestPath(dir, configuration))))
	if err != nil {
		return manifest, err
	}
	if err := decodeArtifact(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Configuration != configuration || !sourceHex(manifest.Identity.SHA256, 32) || (!sourceHex(manifest.BaseRevision, 20) && !sourceHex(manifest.BaseRevision, 32)) {
		return manifest, fmt.Errorf("malformed or mismatched source provenance")
	}
	bundle, err := readFile(dir, sourceBundlePath(manifest.Identity.SHA256))
	if err != nil {
		return manifest, err
	}
	root, err := EvidenceRoot(dir)
	if err != nil {
		return manifest, err
	}
	if err := VerifySourceManifest(ctx, root, manifest, bundle); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// EvidenceRoot resolves the owning repository from its exact longrun fixture path.
func EvidenceRoot(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	root := filepath.Clean(filepath.Join(absolute, "../../.."))
	if filepath.Join(root, "integration_tests/fixtures/longrun") != absolute {
		return "", fmt.Errorf("evidence requires the owning longrun fixture directory")
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		return "", err
	}
	return root, nil
}

// VerifyEvidenceExecutable refuses stale or unsupported binaries before artifact writes.
func VerifyEvidenceExecutable(ctx context.Context, dir string) error {
	root, err := EvidenceRoot(dir)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return VerifySourceExecutable(ctx, root, executable)
}

func sourceManifestPath(dir, configuration string) string {
	return filepath.Join(dir, "gate", "source_"+configuration+".json")
}
func sourceBundlePath(digest string) string {
	return filepath.Join("gate", "source", digest, "source.patch")
}
func sourceHex(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size
}
func validateConfigurationName(configuration string) error {
	switch configuration {
	case "offline", "anthropic", "bedrock-anthropic", "openai", "gemini":
		return nil
	default:
		return fmt.Errorf("unknown provider configuration %q", configuration)
	}
}
func decodeArtifact(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("artifact contains additional JSON values")
		}
		return err
	}
	return nil
}

// ArchiveCurrentV1 identifies the retained committed source for the untouched
// historical artifact before migrating it with its explicit store declaration.
func ArchiveCurrentV1(ctx context.Context, dir, configuration, inventory string) (V1Archive, error) {
	if err := validateConfigurationName(configuration); err != nil {
		return V1Archive{}, err
	}
	root, err := EvidenceRoot(dir)
	if err != nil {
		return V1Archive{}, err
	}
	relative := filepath.ToSlash(filepath.Join("integration_tests/fixtures/longrun/gate", filepath.Base(freezePath(dir, configuration))))
	revision, err := sourceGit(ctx, root, "log", "-1", "--format=%H", "--", relative)
	if err != nil {
		return V1Archive{}, err
	}
	retained := strings.TrimSpace(string(revision))
	if !sourceHex(retained, 20) {
		return V1Archive{}, fmt.Errorf("historical artifact lacks retained source commit")
	}
	original, err := sourceGit(ctx, root, "show", retained+":"+relative)
	if err != nil {
		return V1Archive{}, err
	}
	current, err := readFile(dir, filepath.Join("gate", filepath.Base(relative)))
	if err != nil {
		return V1Archive{}, err
	}
	if !bytes.Equal(original, current) {
		return V1Archive{}, fmt.Errorf("historical artifact differs from retained committed source")
	}
	return ArchiveV1(dir, configuration, inventory, retained)
}
