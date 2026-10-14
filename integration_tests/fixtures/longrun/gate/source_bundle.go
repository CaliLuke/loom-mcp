package suite

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/eval/gate"
)

// SourceManifest separates reproducible semantic input identity from recovery
// provenance. The retained base and bundle stay valid after the source is landed.
type SourceManifest struct {
	Version       int            `json:"version"`
	Configuration string         `json:"configuration"`
	BaseRevision  string         `json:"base_revision"`
	Dirty         bool           `json:"dirty"`
	Identity      SourceIdentity `json:"identity"`
	BundlePaths   []string       `json:"bundle_paths"`
	BundleSHA256  string         `json:"bundle_sha256"`
	SHA256        string         `json:"sha256"`
}

// BuildSourceManifest captures a source-only patch against the supplied retained
// base, including untracked selected owners, and proves its recovery.
func BuildSourceManifest(ctx context.Context, root, configuration, base string) (SourceManifest, []byte, error) {
	var manifest SourceManifest
	baseBytes, err := sourceGit(ctx, root, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return manifest, nil, err
	}
	manifest = SourceManifest{Version: 1, Configuration: configuration, BaseRevision: strings.TrimSpace(string(baseBytes))}
	manifest.Identity, err = SourceSnapshot(ctx, root)
	if err != nil {
		return manifest, nil, err
	}
	for _, owner := range manifest.Identity.Owners {
		manifest.BundlePaths = append(manifest.BundlePaths, owner.Path)
	}
	baseIdentity, err := retainedSourceIdentity(ctx, root, manifest.BaseRevision)
	if err != nil {
		return manifest, nil, err
	}
	for _, owner := range baseIdentity.Owners {
		manifest.BundlePaths = append(manifest.BundlePaths, owner.Path)
	}
	slices.Sort(manifest.BundlePaths)
	manifest.BundlePaths = slices.Compact(manifest.BundlePaths)
	bundle, err := captureSourcePatch(ctx, root, manifest.BaseRevision, baseIdentity.Owners, manifest.Identity.Owners)
	if err != nil {
		return manifest, nil, err
	}
	manifest.Dirty = len(bundle) != 0
	manifest.BundleSHA256 = gate.Digest(bundle)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return manifest, nil, err
	}
	manifest.SHA256 = gate.Digest(encoded)
	if err := VerifySourceManifest(ctx, root, manifest, bundle); err != nil {
		return manifest, nil, err
	}
	return manifest, bundle, nil
}

// VerifySourceManifest checks retained provenance, reconstructs the selected
// source from its base plus bundle, and compares current semantic owner identity.
func VerifySourceManifest(ctx context.Context, root string, manifest SourceManifest, bundle []byte) error {
	expected := manifest.SHA256
	manifest.SHA256 = ""
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if manifest.Version != 1 || expected != gate.Digest(encoded) || manifest.BundleSHA256 != gate.Digest(bundle) || manifest.Configuration == "" {
		return fmt.Errorf("source manifest or bundle checksum mismatch")
	}
	identity := manifest.Identity
	identity.SHA256 = ""
	identityBytes, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if identity.Version != 1 || len(identity.Owners) == 0 || len(identity.Modules) == 0 || len(identity.Build) == 0 || len(identity.Toolchain) == 0 || manifest.Identity.SHA256 != gate.Digest(identityBytes) {
		return fmt.Errorf("source manifest nested identity is incomplete or has an invalid checksum")
	}
	current, err := SourceSnapshot(ctx, root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, manifest.Identity) {
		return fmt.Errorf("current executable source differs from retained manifest")
	}
	temporary, err := os.MkdirTemp("", "compaction-source-recovery-")
	if err != nil {
		return err
	}
	recoveryErr := recoverSource(ctx, root, temporary, manifest, bundle)
	cleanupErr := os.RemoveAll(temporary)
	return errors.Join(recoveryErr, cleanupErr)
}

func sourceGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("missing source operation")
	}
	var command *exec.Cmd
	// Only source inspection and the explicit provenance probe/build operations
	// may launch. Arguments are passed directly; no shell or executable override.
	switch args[0] {
	case "rev-parse":
		command = exec.CommandContext(ctx, "git", "rev-parse")
	case "show":
		command = exec.CommandContext(ctx, "git", "show")
	case "ls-tree":
		command = exec.CommandContext(ctx, "git", "ls-tree")
	case "archive":
		command = exec.CommandContext(ctx, "git", "archive")
	case "log":
		command = exec.CommandContext(ctx, "git", "log")
	default:
		return nil, fmt.Errorf("unsupported source operation %q", args[0])
	}
	command.Args = append(command.Args, args[1:]...)
	command.Dir = root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("source git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return data, nil
}

func recoverSource(ctx context.Context, root, destination string, manifest SourceManifest, bundle []byte) error {
	files, err := sourceGit(ctx, root, "ls-tree", "-r", "--name-only", manifest.BaseRevision)
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for _, path := range strings.Split(strings.TrimSpace(string(files)), "\n") {
		existing[path] = true
	}
	args := []string{"archive", "--format=tar", manifest.BaseRevision, "--"}
	for _, path := range manifest.BundlePaths {
		if existing[path] {
			args = append(args, path)
		}
	}
	if len(args) == 4 {
		return fmt.Errorf("source recovery has no retained base owners")
	}
	archive, err := sourceGit(ctx, root, args...)
	if err != nil {
		return err
	}
	if err := extractSourceArchive(destination, archive); err != nil {
		return err
	}
	if len(bundle) != 0 {
		command := exec.CommandContext(ctx, "git", "apply", "--binary", "-")
		command.Dir = destination
		command.Stdin = bytes.NewReader(bundle)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("recover source patch: %w: %s", err, output)
		}
	}
	recoveryCtx := context.WithValue(ctx, sourceWorkspaceKey{}, filepath.Join(destination, "go.work"))
	recovered, err := SourceSnapshot(recoveryCtx, destination)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(recovered, manifest.Identity) {
		return fmt.Errorf("retained base plus patch does not reconstruct selected source identity: %s", sourceIdentityDifference(manifest.Identity, recovered))
	}
	return nil
}

func retainedSourceIdentity(ctx context.Context, root, base string) (SourceIdentity, error) {
	var identity SourceIdentity
	archive, err := sourceGit(ctx, root, "archive", "--format=tar", base)
	if err != nil {
		return identity, err
	}
	temporary, err := os.MkdirTemp("", "compaction-source-base-")
	if err != nil {
		return identity, err
	}
	extractionErr := extractSourceArchive(temporary, archive)
	if extractionErr == nil {
		baseCtx := context.WithValue(ctx, sourceWorkspaceKey{}, filepath.Join(temporary, "go.work"))
		identity, extractionErr = SourceSnapshot(baseCtx, temporary)
	}
	return identity, errors.Join(extractionErr, os.RemoveAll(temporary))
}

func extractSourceArchive(destination string, archive []byte) (resultErr error) {
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(header.Name) {
			return fmt.Errorf("non-local source archive path")
		}
		path := header.Name
		switch header.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir:
			if err := root.MkdirAll(path, 0750); err != nil {
				return err
			}
		case tar.TypeReg:
			data, err := io.ReadAll(reader)
			if err != nil {
				return err
			}
			if err := root.MkdirAll(filepath.Dir(path), 0750); err != nil {
				return err
			}
			mode := os.FileMode(0644)
			if header.Mode&0111 != 0 {
				mode = 0755
			}
			if err := root.WriteFile(path, data, mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported source archive entry %s", header.Name)
		}
	}
	return nil
}

func sourceIdentityDifference(want, got SourceIdentity) string {
	if !reflect.DeepEqual(want.Owners, got.Owners) {
		expected := make(map[string]string)
		actual := make(map[string]string)
		for _, file := range want.Owners {
			expected[file.Path] = file.SHA256
		}
		for _, file := range got.Owners {
			actual[file.Path] = file.SHA256
		}
		for path, hash := range expected {
			if actual[path] != hash {
				return "owner " + path
			}
		}
		for path := range actual {
			if expected[path] == "" {
				return "extra owner " + path
			}
		}
		return "owner order"
	}
	if !reflect.DeepEqual(want.Modules, got.Modules) {
		for i, module := range want.Modules {
			if i >= len(got.Modules) {
				return "missing module " + module.Path
			}
			if !reflect.DeepEqual(module, got.Modules[i]) {
				return fmt.Sprintf("module %s: expected %+v got %+v", module.Path, module, got.Modules[i])
			}
		}
		return "module count"
	}
	if !reflect.DeepEqual(want.Build, got.Build) {
		return "build settings"
	}
	if !reflect.DeepEqual(want.Toolchain, got.Toolchain) {
		return "toolchain"
	}
	return "metadata"
}
