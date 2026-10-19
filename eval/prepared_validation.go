package eval

import (
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

func clonePreparedReceipt(r *PreparedReceipt) *PreparedReceipt {
	if r == nil {
		return nil
	}
	copy := *r
	copy.Bindings = slices.Clone(r.Bindings)
	slices.SortFunc(copy.Bindings, func(a, b PreparedBinding) int {
		return strings.Compare(a.ID, b.ID)
	})
	if r.Build != nil {
		build := *r.Build
		copy.Build = &build
	}
	return &copy
}

func cloneProbeStatus(p *ProbeStatus) *ProbeStatus {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func validatePreparedReceipt(r PreparedReceipt) error {
	if !validPreparedDigest(r.CandidateDigest) {
		return errors.New("eval: prepared receipt candidate digest must be SHA256")
	}
	for _, value := range []string{r.SurfaceID, r.SurfaceVersion, r.TaskSnapshot, r.SourceSnapshot, r.InputSnapshot, r.ModelConfig, r.Environment.SnapshotID} {
		if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
			return errors.New("eval: prepared receipt requires nonblank UTF-8 identities")
		}
	}
	switch r.Environment.Mode {
	case "fixture", "snapshot", "external_read_only":
	default:
		return errors.New("eval: invalid prepared environment mode")
	}
	if !utf8.ValidString(r.DriverVersion) {
		return errors.New("eval: invalid prepared driver version")
	}
	if len(r.Bindings) == 0 {
		return errors.New("eval: prepared receipt requires bindings")
	}
	seen := make(map[string]bool)
	for _, binding := range r.Bindings {
		if !utf8.ValidString(binding.ID) || !utf8.ValidString(binding.Version) || strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.Version) == "" || seen[binding.ID] {
			return errors.New("eval: invalid or duplicate prepared binding")
		}
		seen[binding.ID] = true
	}
	if b := r.Build; b != nil && (!utf8.ValidString(b.Compiler) || !utf8.ValidString(b.Dependencies) || strings.TrimSpace(b.Compiler) == "" || strings.TrimSpace(b.Dependencies) == "" || !validPreparedDigest(b.ExecutableDigest)) {
		return errors.New("eval: invalid prepared build provenance")
	}
	return nil
}

func validPreparedDigest(value string) bool {
	value = strings.TrimPrefix(value, "sha256:")
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func validateArtifactName(name, mediaType string) error {
	if !utf8.ValidString(name) || name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("eval: invalid artifact name %q", name)
	}
	if _, _, err := mime.ParseMediaType(mediaType); err != nil {
		return fmt.Errorf("eval: invalid artifact media type: %w", err)
	}
	return nil
}

func validateDeliverables(outputs []Deliverable) error {
	seen := make(map[string]bool)
	for _, output := range outputs {
		if err := validateArtifactName(output.Name, output.MediaType); err != nil {
			return err
		}
		if seen[output.Name] {
			return fmt.Errorf("eval: duplicate deliverable %q", output.Name)
		}
		seen[output.Name] = true
	}
	return nil
}
